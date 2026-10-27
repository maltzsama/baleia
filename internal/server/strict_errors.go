package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// The strict handlers below replace oapi-codegen's defaults, which answer
// with the raw error as text/plain and log nothing.

// strictResponseError is where an error a handler returns is handled (#373):
// logged once, with the request id, and answered with a stable code, never
// with the error text, which may carry SQL or internal addresses.
func (s *Server) strictResponseError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "handler error",
		"method", r.Method, "path", r.URL.Path, "request_id", middleware.GetReqID(r.Context()), "err", err)
	writeStrictError(w, http.StatusInternalServerError, "internal_error", "internal error")
}

// strictRequestError answers a request the generated code could not decode
// (malformed body or parameter); the decoder's message is about the input.
func strictRequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeStrictError(w, http.StatusBadRequest, "bad_request", err.Error())
}

func writeStrictError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent; a failed write means the client is gone.
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
