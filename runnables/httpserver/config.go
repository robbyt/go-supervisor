package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ServerCreator is a function type that creates an HttpServer instance
type ServerCreator func(addr string, handler http.Handler, cfg *Config) HttpServer

// DefaultServerCreator creates a standard http.Server instance with the settings from Config
func DefaultServerCreator(addr string, handler http.Handler, cfg *Config) HttpServer {
	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
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

// NewConfig creates a new Config with the required address and routes
// plus any optional configuration via functional options
func NewConfig(addr string, routes Routes, opts ...ConfigOption) (*Config, error) {
	if len(routes) == 0 {
		return nil, errors.New("routes cannot be empty")
	}

	c := &Config{
		ListenAddr: addr,
		Routes:     routes,

		// set some reasonable default values
		DrainTimeout:  30 * time.Second,
		ReadTimeout:   15 * time.Second,
		WriteTimeout:  15 * time.Second,
		IdleTimeout:   1 * time.Minute,
		ServerCreator: DefaultServerCreator,
	}

	// Apply functional options
	for _, opt := range opts {
		opt(c)
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
// Each route's Path is mapped to its Handler function.
func (c *Config) getMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range c.Routes {
		mux.HandleFunc(route.Path, route.Handler)
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
