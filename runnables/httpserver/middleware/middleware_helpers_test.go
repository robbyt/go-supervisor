package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupLogBuffer creates a log buffer and returns it along with a configured logger
func setupLogBuffer(t *testing.T, level slog.Level) (*bytes.Buffer, *slog.Logger) {
	t.Helper()
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{
		Level: level,
	}))
	return &logBuffer, logger
}

// setupRequest creates a test HTTP request
func setupRequest(t *testing.T, method, path string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	return rec, req
}
