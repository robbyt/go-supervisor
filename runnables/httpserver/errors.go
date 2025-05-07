// Package httpserver provides a configurable, reloadable HTTP server implementation
// that can be managed by the supervisor package.
package httpserver

import "errors"

var (
	ErrNoConfig                = errors.New("no config provided")
	ErrNoHandlers              = errors.New("no handlers provided")
	ErrGracefulShutdown        = errors.New("graceful shutdown failed")
	ErrGracefulShutdownTimeout = errors.New("graceful shutdown deadline reached")
	ErrHttpServer              = errors.New("http server error")
	ErrOldConfig               = errors.New("config hasn't changed since last update")
	ErrRetrieveConfig          = errors.New("failed to retrieve server configuration")
	ErrCreateConfig            = errors.New("failed to create server configuration")
	ErrServerNotRunning        = errors.New("http server is not running")
	ErrServerReadinessTimeout  = errors.New("server readiness check timed out")
	ErrServerBoot              = errors.New("failed to start HTTP server")
	ErrConfigCallbackNil       = errors.New("config callback returned nil")
	ErrConfigCallback          = errors.New("failed to load configuration from callback")
	ErrStateTransition         = errors.New("state transition failed")
)
