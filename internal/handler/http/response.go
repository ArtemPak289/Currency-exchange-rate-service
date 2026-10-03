package http

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents a standardized JSON error message.
type ErrorResponse struct {
	Error   string      `json:"error"`
	Details interface{} `json:"details,omitempty"`
}

// JSON writes a JSON response with status code and data.
func JSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// Error writes a JSON error response with status code and error message.
func Error(w http.ResponseWriter, statusCode int, message string) {
	JSON(w, statusCode, ErrorResponse{Error: message})
}

// ErrorWithDetails writes a JSON error response with extra contextual details.
func ErrorWithDetails(w http.ResponseWriter, statusCode int, message string, details interface{}) {
	JSON(w, statusCode, ErrorResponse{
		Error:   message,
		Details: details,
	})
}
