package openlineage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// ErrInsecureBearer indicates that a bearer token would travel over plaintext.
var ErrInsecureBearer = errors.New("bearer token requires HTTPS or a loopback IP")

// ValidateBearerTransport permits plaintext bearer tokens only to literal
// loopback IP addresses; hostnames are not exempt from the HTTPS requirement.
func ValidateBearerTransport(endpoint string, hasToken bool) error {
	if !hasToken {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return ErrInsecureBearer
	}
	if u.Scheme == "https" {
		return nil
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if u.Scheme == "http" && err == nil && ip.IsLoopback() {
		return nil
	}
	return ErrInsecureBearer
}

// HTTPPublisher sends individual events to a configured OpenLineage HTTP endpoint.
// The supplied client must enforce the application's outbound network policy.
type HTTPPublisher struct {
	Client      *http.Client
	Endpoint    string
	BearerToken string
}

// Publish sends an event and reports transport failure without exposing credentials
// or remote response bodies.
func (p HTTPPublisher) Publish(ctx context.Context, event RunEvent) error {
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return errors.New("invalid OpenLineage endpoint")
	}
	if err := ValidateBearerTransport(p.Endpoint, p.BearerToken != ""); err != nil {
		return err
	}
	if p.Client == nil {
		return errors.New("OpenLineage HTTP client is not configured")
	}
	client := *p.Client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode OpenLineage event: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
		if err != nil {
			return errors.New("invalid OpenLineage request")
		}
		req.Header.Set("Content-Type", "application/json")
		if p.BearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+p.BearerToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return fmt.Errorf("OpenLineage endpoint returned HTTP %d", resp.StatusCode)
		}
	}
	return errors.New("OpenLineage request failed after retries")
}
