package api

import (
	"encoding/json"
	"log/slog"
)

// decodeStored unmarshals JSON that Baleia stored into v. An empty value is
// left alone; a corrupt one is logged and v keeps what it had, so one bad row
// does not fail a read but is not silent either (#373).
func decodeStored(raw []byte, v any, what string) {
	if len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, v); err != nil {
		slog.Warn("stored JSON does not decode", "what", what, "err", err)
	}
}
