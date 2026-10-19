// Package mail renders localized transactional e-mails (#67). Only the
// server localizes: API responses carry a stable code, and the UI translates.
package mail

import (
	"bytes"
	"embed"
	"fmt"
	"net/smtp"
	"path"
	"strings"
	"text/template"
)

//go:embed templates
var templates embed.FS

// Config is the SMTP delivery configuration.
type Config struct {
	Host          string
	Port          string
	User          string
	Password      string
	From          string
	DefaultLocale string
}

// Locale resolves a user preference against the instance default.
func Locale(preference *string, fallback string) string {
	if preference != nil {
		switch *preference {
		case "en", "pt-BR":
			return *preference
		}
	}
	if fallback == "pt-BR" {
		return "pt-BR"
	}
	return "en"
}

// Render returns the localized subject and body of a template.
func Render(locale, name string, data any) (subject, body string, err error) {
	if locale != "pt-BR" {
		locale = "en"
	}
	read := func(suffix string) (string, error) {
		raw, err := templates.ReadFile(path.Join("templates", locale, name+suffix))
		if err != nil {
			return "", err
		}
		parsed, err := template.New(name + suffix).Parse(string(raw))
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		if err := parsed.Execute(&buf, data); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	subject, err = read(".subject.txt")
	if err != nil {
		return "", "", err
	}
	body, err = read(".txt")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(subject), body, nil
}

// Send renders and delivers one e-mail. A missing SMTP host is a no-op, as
// before: development stacks run without a mailer.
func Send(cfg Config, to, locale, templateName string, data any) error {
	if cfg.Host == "" || cfg.Port == "" {
		return nil
	}
	subject, body, err := Render(locale, templateName, data)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		cfg.From, to, subject, body,
	)
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	}
	smtpHost := cfg.Host
	if i := strings.IndexByte(smtpHost, ':'); i >= 0 {
		smtpHost = smtpHost[:i]
	}
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Password, smtpHost)
	}
	return smtp.SendMail(cfg.Host+":"+cfg.Port, auth, cfg.From, []string{to}, []byte(msg))
}

// ConfigFrom builds the mailer config from the app configuration values.
func ConfigFrom(host, port, user, password, from, defaultLocale string) Config {
	return Config{Host: host, Port: port, User: user, Password: password, From: from, DefaultLocale: defaultLocale}
}
