package api

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes v as the JSON body of a hand-registered route.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
