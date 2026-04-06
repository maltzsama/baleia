package api

import (
	"fmt"
	"net/smtp"

	"github.com/galileostudio/baleia/internal/config"
)

func sendResetEmail(cfg *config.Config, to, resetURL string) error {
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: Reset your Baleia password\r\n\r\nClick here to reset your password:\r\n%s\r\n",
		cfg.SMTPFrom, to, resetURL,
	)

	addr := cfg.SMTPHost + ":" + cfg.SMTPPort

	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
	}

	return smtp.SendMail(addr, auth, cfg.SMTPFrom, []string{to}, []byte(msg))
}