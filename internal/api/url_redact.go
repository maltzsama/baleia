package api

import (
	"net/url"
	"strings"
)

// sensitiveQueryParams are query parameter names whose values may carry secrets.
var sensitiveQueryParams = map[string]bool{
	"password": true, "passwd": true, "pwd": true,
	"token": true, "access_token": true, "api_key": true, "apikey": true,
	"secret": true, "client_secret": true,
	"sas": true, "sig": true, "signature": true,
}

// redactURLSecrets removes userinfo and masks secret-bearing query parameter
// values so a stored connection URL never returns embedded credentials to a
// caller. It understands plain URLs and jdbc: URLs, and leaves a URL it cannot
// improve unchanged.
func redactURLSecrets(raw string) string {
	if raw == "" {
		return raw
	}

	prefix := ""
	rest := raw
	if strings.HasPrefix(rest, "jdbc:") {
		prefix = "jdbc:"
		rest = strings.TrimPrefix(rest, "jdbc:")
	}

	u, err := url.Parse(rest)
	if err != nil || u.Host == "" {
		return raw
	}

	changed := false
	if u.User != nil {
		u.User = url.User("redacted")
		changed = true
	}
	if u.RawQuery != "" {
		q := u.Query()
		queryChanged := false
		for key := range q {
			if sensitiveQueryParams[strings.ToLower(key)] {
				q.Set(key, "redacted")
				queryChanged = true
			}
		}
		if queryChanged {
			u.RawQuery = q.Encode()
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return prefix + u.String()
}
