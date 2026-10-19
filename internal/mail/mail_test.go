package mail

import (
	"strings"
	"testing"
)

func TestTemplatesBothLocales(t *testing.T) {
	for _, locale := range []string{"en", "pt-BR"} {
		subject, body, err := Render(locale, "password_reset", map[string]string{"ResetURL": "https://example.test/reset"})
		if err != nil {
			t.Fatalf("%s password_reset: %v", locale, err)
		}
		if subject == "" || !strings.Contains(body, "https://example.test/reset") {
			t.Fatalf("%s password_reset rendered %q / %q", locale, subject, body)
		}
		subject, body, err = Render(locale, "invite", map[string]string{"InviteURL": "https://example.test/invite"})
		if err != nil {
			t.Fatalf("%s invite: %v", locale, err)
		}
		if subject == "" || !strings.Contains(body, "https://example.test/invite") {
			t.Fatalf("%s invite rendered %q / %q", locale, subject, body)
		}
	}

	en, _, _ := Render("en", "password_reset", map[string]string{"ResetURL": "x"})
	pt, _, _ := Render("pt-BR", "password_reset", map[string]string{"ResetURL": "x"})
	if en == pt {
		t.Fatal("the subjects should differ between locales")
	}
}
