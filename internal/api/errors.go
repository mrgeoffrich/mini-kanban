package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

type errorBody struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: msg, Code: code, Details: details})
}

// statusForError maps a store/decoder error onto an HTTP status + machine
// code. The string match for UNIQUE constraints is necessary because
// modernc.org/sqlite surfaces them as text rather than a typed sentinel.
func statusForError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound, "not_found"
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge, "payload_too_large"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return http.StatusBadRequest, "invalid_input"
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return http.StatusBadRequest, "invalid_input"
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return http.StatusConflict, "conflict"
	}
	if strings.Contains(msg, "unknown field") {
		return http.StatusBadRequest, "invalid_input"
	}
	return http.StatusInternalServerError, "internal"
}
