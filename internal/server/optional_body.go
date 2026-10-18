package server

import (
	"io"
	"net/http"
	"regexp"
	"strings"
)

// optionalBodyRoutes are the operations whose request body the API spec makes
// optional (TestOptionalBodyRoutesMatchSpec). The generated strict handlers
// always decode a body, so an empty one would be refused with 400.
var optionalBodyRoutes = []*regexp.Regexp{
	regexp.MustCompile(`^/api/v1/auth/logout$`),
	regexp.MustCompile(`^/api/v1/auth/refresh$`),
	regexp.MustCompile(`^/api/v1/ws-tickets$`),
	regexp.MustCompile(`^/api/v1/workspaces/[^/]+/retrievers/[^/]+/refresh$`),
	regexp.MustCompile(`^/api/v1/users/[^/]+/suspend$`),
}

// OptionalJSONBody gives a POST to those operations an empty JSON object
// when the client sent no body, as the spec allows.
func OptionalJSONBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.ContentLength <= 0 && matchesOptionalBody(r.URL.Path) {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err == nil && len(body) == 0 {
				r.Body = io.NopCloser(strings.NewReader("{}"))
				r.ContentLength = 2
				if r.Header.Get("Content-Type") == "" {
					r.Header.Set("Content-Type", "application/json")
				}
			} else {
				// A chunked body: put back what was read.
				r.Body = io.NopCloser(io.MultiReader(strings.NewReader(string(body)), r.Body))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func matchesOptionalBody(path string) bool {
	for _, re := range optionalBodyRoutes {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}
