package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStrictResponseError: a handler error is answered with a stable code,
// never with its text (#373).
func TestStrictResponseError(t *testing.T) {
	rec := httptest.NewRecorder()
	strictResponseError(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), errors.New(`pq: relation "secret_table" does not exist`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}
	if body["code"] != "internal_error" || body["message"] != "internal error" {
		t.Fatalf("body = %v, want the generic internal_error", body)
	}
}

// TestStrictRequestError: a decode error is a JSON bad_request, and a
// message with quotes still makes valid JSON.
func TestStrictRequestError(t *testing.T) {
	rec := httptest.NewRecorder()
	strictRequestError(rec, httptest.NewRequest(http.MethodPost, "/api/v1/x", nil), errors.New(`can't decode JSON body: invalid character '"'`))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest || body["code"] != "bad_request" {
		t.Fatalf("status %d body %v, want 400 bad_request", rec.Code, body)
	}
}
