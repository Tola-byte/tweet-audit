package logger

import (
	"context"
	"os"

	charmlog "github.com/charmbracelet/log"
)

var defaultLogger = charmlog.NewWithOptions(os.Stdout, charmlog.Options{
	ReportTimestamp: true,
	TimeFormat:      "15:04:05",
	Level:           charmlog.DebugLevel,
})

// Logger wraps charmbracelet/log with structured field support
type Logger struct {
	l *charmlog.Logger
}

func New() *Logger {
	return &Logger{l: defaultLogger}
}

func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &Logger{l: l.l.With(args...)}
}

func (l *Logger) WithContext(ctx context.Context) *Logger {
	args := []interface{}{}
	if jobID, ok := ctx.Value("job_id").(string); ok {
		args = append(args, "job_id", jobID)
	}
	if fileID, ok := ctx.Value("file_id").(string); ok {
		args = append(args, "file_id", fileID)
	}
	if len(args) > 0 {
		return &Logger{l: l.l.With(args...)}
	}
	return l
}

func (l *Logger) Debug(msg string, args ...interface{}) { l.l.Debugf(msg, args...) }
func (l *Logger) Info(msg string, args ...interface{})  { l.l.Infof(msg, args...) }
func (l *Logger) Warn(msg string, args ...interface{})  { l.l.Warnf(msg, args...) }
func (l *Logger) Error(msg string, args ...interface{}) { l.l.Errorf(msg, args...) }

// Package-level functions
func WithFields(fields map[string]interface{}) *Logger { return New().WithFields(fields) }
func WithContext(ctx context.Context) *Logger          { return New().WithContext(ctx) }
func WithJobID(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, "job_id", jobID)
}
func WithFileID(ctx context.Context, fileID string) context.Context {
	return context.WithValue(ctx, "file_id", fileID)
}

func Debug(msg string, args ...interface{}) { defaultLogger.Debugf(msg, args...) }
func Info(msg string, args ...interface{})  { defaultLogger.Infof(msg, args...) }
func Warn(msg string, args ...interface{})  { defaultLogger.Warnf(msg, args...) }
func Error(msg string, args ...interface{}) { defaultLogger.Errorf(msg, args...) }
