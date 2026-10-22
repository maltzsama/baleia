package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
)

func loginHandler(token string) api.StrictHandlerFunc {
	return func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return api.Login200JSONResponse{RefreshToken: &token}, nil
	}
}

// TestRefreshCookie_Flags: the cookie is HttpOnly, Secure, SameSite=Lax and
// scoped to the auth routes (#51).
func TestRefreshCookie_Flags(t *testing.T) {
	cfg := &config.Config{RefreshTokenTTL: time.Hour}
	handler := refreshCookieMiddleware(cfg)(loginHandler("tok-1"), "Login")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	resp, err := handler(context.Background(), rec, req, nil)
	if err != nil {
		t.Fatal(err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != auth.RefreshCookieName || c.Value != "tok-1" {
		t.Errorf("cookie = %s=%q", c.Name, c.Value)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("flags: HttpOnly=%v Secure=%v SameSite=%v", c.HttpOnly, c.Secure, c.SameSite)
	}
	if c.Path != auth.RefreshCookiePath {
		t.Errorf("path = %q, want %q", c.Path, auth.RefreshCookiePath)
	}
	if c.MaxAge != int(time.Hour.Seconds()) {
		t.Errorf("max-age = %d, want %d", c.MaxAge, int(time.Hour.Seconds()))
	}
	if login, ok := resp.(api.Login200JSONResponse); !ok || login.RefreshToken != nil {
		t.Errorf("browser response leaked the refresh token: %#v", resp)
	}
}

// TestRefreshCookie_NotInJSONForBrowser: only X-Baleia-Client: api receives the
// JSON refresh token (#51).
func TestRefreshCookie_NotInJSONForBrowser(t *testing.T) {
	cfg := &config.Config{}
	mw := refreshCookieMiddleware(cfg)

	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"browser", "", false},
		{"other client", "web", false},
		{"api client", auth.ClientAPI, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			if tc.header != "" {
				req.Header.Set(auth.ClientHeader, tc.header)
			}
			resp, err := mw(loginHandler("tok-1"), "Login")(context.Background(), rec, req, nil)
			if err != nil {
				t.Fatal(err)
			}
			login := resp.(api.Login200JSONResponse)
			if got := login.RefreshToken != nil; got != tc.want {
				t.Errorf("refresh_token present = %v, want %v", got, tc.want)
			}
			// The cookie is always set; the JSON field is what is gated.
			if len(rec.Result().Cookies()) != 1 {
				t.Error("the refresh cookie was not set")
			}
		})
	}
}

// TestLogout_ClearsCookie: logout emits an expired cookie (#51).
func TestLogout_ClearsCookie(t *testing.T) {
	cfg := &config.Config{}
	handler := refreshCookieMiddleware(cfg)(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return api.Logout204Response{}, nil
	}, "Logout")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	if _, err := handler(context.Background(), rec, req, nil); err != nil {
		t.Fatal(err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != auth.RefreshCookieName || c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("clear cookie = %+v", c)
	}
	if !c.HttpOnly || !c.Secure || c.Path != auth.RefreshCookiePath {
		t.Errorf("clear cookie flags = %+v", c)
	}
}

func TestRefreshCookieContext(t *testing.T) {
	var got string
	h := refreshCookieContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = auth.RefreshCookieFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: "tok-1"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "tok-1" {
		t.Errorf("context cookie = %q, want tok-1", got)
	}
}
