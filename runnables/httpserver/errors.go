// Package httpserver provides a configurable, reloadable HTTP server implementation
// that can be managed by the supervisor package.
package httpserver

import "errors"

// ErrNoConfig is returned when no configuration is provided to the server
var ErrNoConfig = errors.New("no config provided")

// ErrNoHandlers is returned when no HTTP handlers are provided in the configuration
var ErrNoHandlers = errors.New("no handlers provided")

// ErrGracefulShutdown is returned when the server fails to shut down gracefully
var ErrGracefulShutdown = errors.New("graceful shutdown failed")

// ErrGracefulShutdownTimeout is returned when the server shutdown exceeds the configured drain timeout
var ErrGracefulShutdownTimeout = errors.New("graceful shutdown deadline reached")

// ErrHttpServer is returned when the HTTP server encounters an operational error
var ErrHttpServer = errors.New("http server error")

// ErrOldConfig is returned when a reload is attempted but the configuration hasn't changed
var ErrOldConfig = errors.New("config hasn't changed since last update")
