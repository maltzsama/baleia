// Package mail renders and sends the small set of transactional e-mails (#67).
//
// The API itself is English-only: it returns stable error codes for the UI to
// translate. E-mail is the one server-localized surface (there is no client to
// translate it), so templates live under templates/<locale>/ and use the
// recipient's users.locale, falling back to BALEIA_DEFAULT_LOCALE.
package mail

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"text/template"
)

// Template names one transactional e-mail.
type Template string

const (
	TemplatePasswordReset Template = "password_reset"
	TemplateInvite        Template = "invite"
)

const (
	LocaleEnglish = "en"
	LocalePtBR    = "pt-BR"
)

//go:embed templates
var templates embed.FS

// Config is the SMTP configuration plus the fallback locale.
type Config struct {
	Host          string
	Port          string
	User          string
	Password      string
	From          string
	DefaultLocale string
}

// Message is a rendered e-mail.
type Message struct {
	Subject string
	Body    string
}

// Sender renders templates and sends them over SMTP.
type Sender struct {
	cfg Config
}

// New returns a Sender. A Sender with no Host/Port skips delivery and logs.
func New(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

// Supported reports whether the locale has templates.
func Supported(locale string) bool {
	return locale == LocaleEnglish || locale == LocalePtBR
}

// LocaleOrDefault resolves the recipient preference, then the server default,
// then English. An unsupported users.locale value cannot reach this point
// (the column has a CHECK), but a bad env var can.
func LocaleOrDefault(preference *string, fallback string) string {
	if preference != nil && Supported(*preference) {
		return *preference
	}
	if Supported(fallback) {
		return fallback
	}
	return LocaleEnglish
}

// Render builds the localized subject and plain-text body.
func Render(locale string, name Template, data map[string]any) (Message, error) {
	if !Supported(locale) {
		locale = LocaleEnglish
	}
	subject, err := renderPart(locale, name, "subject", data)
	if err != nil {
		return Message{}, err
	}
	body, err := renderPart(locale, name, "txt", data)
	if err != nil {
		return Message{}, err
	}
	return Message{Subject: strings.TrimSpace(subject), Body: strings.TrimSpace(body)}, nil
}

func renderPart(locale string, name Template, ext string, data map[string]any) (string, error) {
	path := "templates/" + locale + "/" + string(name) + "." + ext
	raw, err := templates.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("mail: missing template %s: %w", path, err)
	}
	tmpl, err := template.New(path).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("mail: parse %s: %w", path, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("mail: render %s: %w", path, err)
	}
	return out.String(), nil
}

// Send renders the message in the given locale and delivers it. Without SMTP
// configuration it logs and returns nil, as before (#51).
func (s *Sender) Send(ctx context.Context, to, locale string, name Template, data map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message, err := Render(locale, name, data)
	if err != nil {
		return err
	}
	if s.cfg.Host == "" || s.cfg.Port == "" {
		slog.Warn("SMTP not configured, skipping email", "to", to, "template", name)
		return nil
	}

	body := strings.ReplaceAll(message.Body, "\n", "\r\n")
	raw := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		s.cfg.From, to, message.Subject, body,
	)

	var auth smtp.Auth
	if s.cfg.User != "" {
		auth = smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)
	}

	addr := s.cfg.Host + ":" + s.cfg.Port
	return smtp.SendMail(addr, auth, s.cfg.From, []string{to}, []byte(raw))
}
