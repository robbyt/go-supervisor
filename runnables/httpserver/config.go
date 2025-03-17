package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Config is the main configuration struct for the HTTP server
type Config struct {
	ListenAddr   string
	DrainTimeout time.Duration
	Routes       Routes
}

// NewConfig returns a new Config instance with the specified address,
// drainTimeout (how long to wait for connections to complete during shutdown), and routes
func NewConfig(addr string, drainTimeout time.Duration, routes Routes) (*Config, error) {
	if len(routes) == 0 {
		return nil, errors.New("routes cannot be empty")
	}

	return &Config{
		ListenAddr:   addr,
		DrainTimeout: drainTimeout,
		Routes:       routes,
	}, nil
}

// String returns a human-readable representation of the Config
func (c *Config) String() string {
	return fmt.Sprintf("Config<addr=%s, drainTimeout=%s, routes=%s>", c.ListenAddr, c.DrainTimeout, c.Routes)
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

// Equal compares this Config with another and returns true if they are equivalent.
// Two configs are considered equal if they have the same ListenAddr, DrainTimeout, and Routes.
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

	return true
}
