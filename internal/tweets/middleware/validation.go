package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tweet-audit/internal/tweets/logger"
	"tweet-audit/internal/tweets/util"
)

type ValidationConfig struct {
	MaxUploadSize int64
	AllowedFileTypes []string
}

func NewValidationMiddleware(cfg ValidationConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/tweets/upload") {
				if err := validateUploadRequest(r, cfg); err != nil {
					logger.Warn("Upload validation failed: %v", err)
					util.WriteError(w, http.StatusBadRequest, err.Error())
					return
				}
			}

			if err := validateQueryParams(r); err != nil {
				logger.Warn("Query param validation failed: %v", err)
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func validateUploadRequest(r *http.Request, cfg ValidationConfig) error {
	if r.ContentLength > cfg.MaxUploadSize {
		return &ValidationError{
			Field:   "file",
			Message: fmt.Sprintf("file size exceeds maximum allowed size of %d bytes", cfg.MaxUploadSize),
		}
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return &ValidationError{
			Field:   "Content-Type",
			Message: "request must be multipart/form-data",
		}
	}

	return nil
}

func validateQueryParams(r *http.Request) error {
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		page, err := strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			return &ValidationError{
				Field:   "page",
				Message: "page must be a positive integer",
			}
		}
	}

	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		pageSize, err := strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return &ValidationError{
				Field:   "page_size",
				Message: "page_size must be between 1 and 100",
			}
		}
	}

	if format := r.URL.Query().Get("format"); format != "" {
		if format != "json" && format != "csv" {
			return &ValidationError{
				Field:   "format",
				Message: "format must be 'json' or 'csv'",
			}
		}
	}

	return nil
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s - %s", e.Field, e.Message)
}
