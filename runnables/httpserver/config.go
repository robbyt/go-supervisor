package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const (
	defaultDrainTimeout = 30 * time.Second
	defaultReadTimeout  = 15 * time.Second
	defaultWriteTimeout = 15 * time.Second
	defaultIdleTimeout  = 1 * time.Minute
)

// ServerCreator is a function type that creates an HttpServer instance
type ServerCreator func(addr string, handler http.Handler, cfg *Config) HttpServer

// DefaultServerCreator creates a standard http.Server instance with the settings from Config
func DefaultServerCreator(addr string, handler http.Handler, cfg *Config) HttpServer {
	// Determine which context to use
	ctx := cfg.context
	if ctx == nil {
		ctx = context.Background()
	}

	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}
}

// Config is the main configuration struct for the HTTP server
type Config struct {
	// Core configuration
	ListenAddr   string
	DrainTimeout time.Duration
	Routes       Routes

	// Server settings
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// Server creation callback function
	ServerCreator ServerCreator

	// Context for request handlers
	context context.Context
}

// ConfigOption defines a functional option for configuring Config
type ConfigOption func(*Config)

// WithDrainTimeout sets the drain timeout for graceful shutdown
func WithDrainTimeout(timeout time.Duration) ConfigOption {
	return func(c *Config) {
		c.DrainTimeout = timeout
	}
}

// WithReadTimeout sets the read timeout for the HTTP server
func WithReadTimeout(timeout time.Duration) ConfigOption {
	return func(c *Config) {
		c.ReadTimeout = timeout
	}
}

// WithWriteTimeout sets the write timeout for the HTTP server
func WithWriteTimeout(timeout time.Duration) ConfigOption {
	return func(c *Config) {
		c.WriteTimeout = timeout
	}
}

// WithIdleTimeout sets the idle timeout for the HTTP server
func WithIdleTimeout(timeout time.Duration) ConfigOption {
	return func(c *Config) {
		c.IdleTimeout = timeout
	}
}

// WithServerCreator sets a custom server creator for the HTTP server
func WithServerCreator(creator ServerCreator) ConfigOption {
	return func(c *Config) {
		if creator != nil {
			c.ServerCreator = creator
		}
	}
}

// WithRequestContext sets the context that will be propagated to all request handlers
// via http.Server's BaseContext. This allows handlers to be aware of server shutdown.
func WithRequestContext(ctx context.Context) ConfigOption {
	return func(c *Config) {
		if ctx != nil {
			c.context = ctx
		}
	}
}

// WithConfigCopy creates a ConfigOption that copies most settings from the source config
// except for ListenAddr and Routes which are provided directly to NewConfig.
func WithConfigCopy(src *Config) ConfigOption {
	return func(dst *Config) {
		if src == nil {
			return
		}

		// Copy timeout settings
		dst.DrainTimeout = src.DrainTimeout
		dst.ReadTimeout = src.ReadTimeout
		dst.WriteTimeout = src.WriteTimeout
		dst.IdleTimeout = src.IdleTimeout

		// Copy other settings
		dst.ServerCreator = src.ServerCreator
		dst.context = src.context
	}
}

// validateRoutePatterns surfaces http.ServeMux pattern conflicts at
// construction time by pre-registering each route against a throwaway mux.
// ServeMux.Handle panics on identical patterns and (Go 1.22+) on conflicting
// patterns where neither is more specific (e.g. "/a/{x}/b" vs "/a/y/{z}");
// running the same registration here lets us recover that panic and return
// it as a normal error instead of letting the server crash at start.
func validateRoutePatterns(routes Routes) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid or conflicting route patterns: %v", r)
		}
	}()
	mux := http.NewServeMux()
	noopHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, route := range routes {
		mux.Handle(route.Path, noopHandler)
	}
	return nil
}

// NewConfig creates a new Config with the address and routes
// plus any optional configuration via functional options.
//
// All validation runs after the supplied ConfigOptions have been applied,
// so options that mutate Routes/ListenAddr/timeouts are still bound by the
// same checks as the caller's positional arguments.
func NewConfig(addr string, routes Routes, opts ...ConfigOption) (*Config, error) {
	// Use constants for default values
	c := &Config{
		ListenAddr:    addr,
		Routes:        routes,
		DrainTimeout:  defaultDrainTimeout,
		ReadTimeout:   defaultReadTimeout,
		WriteTimeout:  defaultWriteTimeout,
		IdleTimeout:   defaultIdleTimeout,
		ServerCreator: DefaultServerCreator,
		context:       context.Background(),
	}

	// Apply overrides from the functional options
	for _, opt := range opts {
		opt(c)
	}

	if len(c.Routes) == 0 {
		return nil, errors.New("routes cannot be empty")
	}
	if c.ListenAddr == "" {
		return nil, errors.New("addr cannot be empty")
	}
	// Byte-for-byte duplicates: report every duplicated path, not just the first,
	// so a config with several typos surfaces them all at once. Each duplicated
	// path is reported once regardless of how many copies appear.
	seen := make(map[string]int, len(c.Routes))
	var dupErrs []error
	for _, r := range c.Routes {
		seen[r.Path]++
		if seen[r.Path] == 2 {
			dupErrs = append(dupErrs, fmt.Errorf("duplicate route path: %q", r.Path))
		}
	}
	if len(dupErrs) > 0 {
		return nil, errors.Join(dupErrs...)
	}
	// Go 1.22+ http.ServeMux also panics on overlapping non-identical patterns
	// (e.g. "/a/{x}/b" vs "/a/y/{z}"); validateRoutePatterns catches that class.
	if err := validateRoutePatterns(c.Routes); err != nil {
		return nil, err
	}
	if c.DrainTimeout < 0 {
		return nil, fmt.Errorf("DrainTimeout must be >= 0, got %s", c.DrainTimeout)
	}
	if c.ReadTimeout < 0 {
		return nil, fmt.Errorf("ReadTimeout must be >= 0, got %s", c.ReadTimeout)
	}
	if c.WriteTimeout < 0 {
		return nil, fmt.Errorf("WriteTimeout must be >= 0, got %s", c.WriteTimeout)
	}
	if c.IdleTimeout < 0 {
		return nil, fmt.Errorf("IdleTimeout must be >= 0, got %s", c.IdleTimeout)
	}

	return c, nil
}

// String returns a human-readable representation of the Config
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config<addr=%s, drainTimeout=%s, routes=%s, timeouts=[read=%s,write=%s,idle=%s]>",
		c.ListenAddr,
		c.DrainTimeout,
		c.Routes,
		c.ReadTimeout,
		c.WriteTimeout,
		c.IdleTimeout,
	)
}

// Equal compares this Config with another and returns true if they are equivalent.
func (c *Config) Equal(other *Config) bool {
	if other == nil {
		return false
	}

	if c.ListenAddr != other.ListenAddr {
		return false
	}

	if c.DrainTimeout != other.DrainTimeout {
		return false
	}

	if !c.Routes.Equal(other.Routes) {
		return false
	}

	// Compare server settings
	if c.ReadTimeout != other.ReadTimeout {
		return false
	}

	if c.WriteTimeout != other.WriteTimeout {
		return false
	}

	if c.IdleTimeout != other.IdleTimeout {
		return false
	}

	// Note: We don't compare ServerCreator functions as they're not directly comparable

	return true
}

// getMux creates and returns a new http.ServeMux with all configured routes registered.
// Each route's Path is mapped to its handler chain via ServeHTTP.
func (c *Config) getMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range c.Routes {
		mux.Handle(route.Path, &route)
	}
	return mux
}

// createServer creates an HTTP server using the configuration's settings
func (c *Config) createServer() HttpServer {
	addr := c.ListenAddr
	mux := c.getMux()
	creator := c.ServerCreator
	if creator == nil {
		creator = DefaultServerCreator
	}

	return creator(addr, mux, c)
}
