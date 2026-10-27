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
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)
	return sendMail(ctx, addr, s.cfg.Host, auth, s.cfg.From, to, []byte(raw))
}

// sendMail is smtp.SendMail bounded by ctx. smtp.SendMail takes no context
// and sets no deadline, so a server that accepts and stops answering held a
// delivery, and the shutdown that waits for it, forever (#422). Here the
// connection carries ctx's deadline and is closed when ctx ends.
func sendMail(ctx context.Context, addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	// As smtp.SendMail: a CR or LF in an address would inject commands.
	if strings.ContainsAny(from, "\r\n") || strings.ContainsAny(to, "\r\n") {
		return errors.New("smtp: address contains CR or LF")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer func() { _ = c.Close() }()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not support AUTH")
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data end: %w", err)
	}
	return c.Quit()
}
