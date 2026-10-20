package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/mail"
)

// mailerFunc adapts a function to MailSender in tests.
type mailerFunc func(context.Context, string, string, mail.Template, map[string]any) error

func (f mailerFunc) Send(ctx context.Context, to, locale string, template mail.Template, data map[string]any) error {
	return f(ctx, to, locale, template, data)
}
