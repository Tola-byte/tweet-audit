package util

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// ParseInt parses a string to int with a default value
// Returns defaultValue if string is empty or invalid
func ParseInt(s string, defaultValue int) int {
	if s == "" {
		return defaultValue
	}
	result, err := strconv.Atoi(s)
	if err != nil || result < 1 {
		return defaultValue
	}
	return result
}

// ParseIntWithMax parses a string to int with default and max values
func ParseIntWithMax(s string, defaultValue, max int) int {
	val := ParseInt(s, defaultValue)
	if val > max {
		return max
	}
	return val
}

// ExtractPathParam extracts a path parameter from URL path
// Example: ExtractPathParam("/jobs/abc123", "/jobs/") returns "abc123"
func ExtractPathParam(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.TrimPrefix(path, prefix)
}

// WriteJSON writes a JSON response with proper headers
func WriteJSON(w http.ResponseWriter, statusCode int, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(data)
}

// WriteError writes an error response
func WriteError(w http.ResponseWriter, statusCode int, message string) {
	http.Error(w, message, statusCode)
}

// GetQueryParam gets a query parameter with optional default
func GetQueryParam(r *http.Request, key, defaultValue string) string {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultValue
	}
	return val
}

// MethodHandler wraps an http.HandlerFunc to only allow specific HTTP methods
// Returns 405 Method Not Allowed if method doesn't match
func MethodHandler(allowedMethods ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			for _, method := range allowedMethods {
				if r.Method == method {
					next(w, r)
					return
				}
			}
			// Method not allowed
			w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}
