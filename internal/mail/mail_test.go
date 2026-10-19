package mail

import (
	"context"
	"strings"
	"testing"
)

func TestTemplatesBothLocales(t *testing.T) {
	data := map[string]any{
		"Name":      "Ana",
		"ResetURL":  "https://baleia.test/reset-password?token=abc",
		"InviteURL": "https://baleia.test/accept-invite?token=abc",
	}
	for _, template := range []Template{TemplatePasswordReset, TemplateInvite} {
		t.Run(string(template), func(t *testing.T) {
			en, err := Render(LocaleEnglish, template, data)
			if err != nil {
				t.Fatalf("en: %v", err)
			}
			pt, err := Render(LocalePtBR, template, data)
			if err != nil {
				t.Fatalf("pt-BR: %v", err)
			}
			for locale, message := range map[string]Message{"en": en, "pt-BR": pt} {
				if message.Subject == "" || message.Body == "" {
					t.Fatalf("%s/%s: empty message", locale, template)
				}
				if strings.Contains(message.Subject, "{{") || strings.Contains(message.Body, "{{") {
					t.Errorf("%s/%s: unrendered template", locale, template)
				}
			}
			if en.Subject == pt.Subject {
				t.Errorf("%s: the subject did not change between locales", template)
			}
			if !strings.Contains(pt.Body, data["ResetURL"].(string)) &&
				!strings.Contains(pt.Body, data["InviteURL"].(string)) {
				t.Errorf("%s: the link is missing from the localized body", template)
			}
		})
	}
}

func TestRenderRejectsUnknownTemplate(t *testing.T) {
	if _, err := Render(LocaleEnglish, Template("nope"), nil); err == nil {
		t.Fatal("an unknown template must fail")
	}
}

func TestRenderRejectsMissingData(t *testing.T) {
	if _, err := Render(LocaleEnglish, TemplatePasswordReset, map[string]any{"Name": "Ana"}); err == nil {
		t.Fatal("a missing template value must fail")
	}
}

func TestLocaleOrDefault(t *testing.T) {
	pt := LocalePtBR
	cases := []struct {
		name       string
		preference *string
		fallback   string
		want       string
	}{
		{"preference wins", &pt, "en", "pt-BR"},
		{"fallback when unset", nil, "pt-BR", "pt-BR"},
		{"english when both unset", nil, "", "en"},
		{"invalid preference falls back", ptr("fr"), "pt-BR", "pt-BR"},
		{"invalid fallback is english", nil, "fr", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocaleOrDefault(tc.preference, tc.fallback); got != tc.want {
				t.Fatalf("LocaleOrDefault = %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestSendSkipsWithoutSMTP(t *testing.T) {
	sender := New(Config{})
	if err := sender.Send(context.Background(), "ana@baleia.test", LocaleEnglish, TemplateInvite,
		map[string]any{"Name": "Ana", "InviteURL": "https://baleia.test/x"}); err != nil {
		t.Fatalf("Send without SMTP = %v, want nil", err)
	}
}
