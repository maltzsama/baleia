//go:build e2e

// Command authproxy adds the short-lived OpenMetadata test token on the host.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

func main() {
	data, err := os.ReadFile(os.Getenv("OPENMETADATA_LOGIN_FILE"))
	if err != nil {
		log.Fatal(err)
	}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &login); err != nil || login.AccessToken == "" {
		log.Fatal("invalid OpenMetadata login")
	}
	target, err := url.Parse(os.Getenv("OPENMETADATA_PROXY_TARGET"))
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(r *http.Request) { original(r); r.Header.Set("Authorization", "Bearer "+login.AccessToken) }
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/openlineage/lineage" {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: os.Getenv("OPENMETADATA_PROXY_BIND"), Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
