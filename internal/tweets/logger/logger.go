package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Logger provides structured logging similar to NestJS
type Logger struct {
	*slog.Logger
}

// New creates a new logger with structured output
func New() *Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug, // Show all logs including debug
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize timestamp format
			if a.Key == slog.TimeKey {
				return slog.String("time", a.Value.Time().Format(time.RFC3339))
			}
			return a
		},
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	return &Logger{
		Logger: slog.New(handler),
	}
}

// WithContext adds context fields to the logger
func (l *Logger) WithContext(ctx context.Context) *Logger {
	
	args := []interface{}{}

	if jobID, ok := ctx.Value("job_id").(string); ok {
		args = append(args, "job_id", jobID)
	}
	if fileID, ok := ctx.Value("file_id").(string); ok {
		args = append(args, "file_id", fileID)
	}

	if len(args) > 0 {
		return &Logger{Logger: l.Logger.With(args...)}
	}
	return l
}

// WithFields adds custom fields to the logger
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &Logger{Logger: l.Logger.With(args...)}
}

// Convenience methods that match NestJS-style logging

func (l *Logger) Debug(msg string, args ...interface{}) {
	l.Logger.Debug(fmt.Sprintf(msg, args...))
}

func (l *Logger) Info(msg string, args ...interface{}) {
	l.Logger.Info(fmt.Sprintf(msg, args...))
}

func (l *Logger) Warn(msg string, args ...interface{}) {
	l.Logger.Warn(fmt.Sprintf(msg, args...))
}

func (l *Logger) Error(msg string, args ...interface{}) {
	l.Logger.Error(fmt.Sprintf(msg, args...))
}

func WithJobID(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, "job_id", jobID)
}

func WithFileID(ctx context.Context, fileID string) context.Context {
	return context.WithValue(ctx, "file_id", fileID)
}

// Global logger instance (can be replaced for testing)
var defaultLogger = New()

// Package-level convenience functions
func Debug(msg string, args ...interface{}) {
	defaultLogger.Debug(msg, args...)
}

func Info(msg string, args ...interface{}) {
	defaultLogger.Info(msg, args...)
}

func Warn(msg string, args ...interface{}) {
	defaultLogger.Warn(msg, args...)
}

func Error(msg string, args ...interface{}) {
	defaultLogger.Error(msg, args...)
}

func WithContext(ctx context.Context) *Logger {
	return defaultLogger.WithContext(ctx)
}

func WithFields(fields map[string]interface{}) *Logger {
	return defaultLogger.WithFields(fields)
}
