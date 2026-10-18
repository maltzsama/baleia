package auth

import (
	"net/http"
	"time"
)

// SetRefreshCookie writes the browser session cookie (#51/#252). Secure is
// always set: browsers treat http://localhost as a trustworthy origin, so local
// development still works.
func SetRefreshCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	if token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    token,
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl / time.Second),
	})
}

// ClearRefreshCookie expires the browser session cookie.
func ClearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
