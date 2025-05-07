package httpserver

import (
	"fmt"
	"net"
	"time"
)

// managedListener is a wrapper around net.Listener that includes additional metadata
type managedListener struct {
	net.Listener
	originalAddr string // The originally requested address (before OS binding)
	createTime   time.Time
}

// newManagedListener creates a new managed listener wrapper
func newManagedListener(listener net.Listener, originalAddr string) *managedListener {
	return &managedListener{
		Listener:     listener,
		originalAddr: originalAddr,
		createTime:   time.Now(),
	}
}

// isListenerValid checks if a listener is still valid and can be reused for the given address
func isListenerValid(listener net.Listener, addr string) bool {
	if listener == nil {
		return false
	}

	// Try to get metadata if this is our managed listener
	if ml, ok := listener.(*managedListener); ok {
		// If the original requested address matches, we can reuse
		// This handles cases where a listener was bound with a dynamic port (:0)
		if ml.originalAddr == addr {
			return true
		}
	}

	// Fall back to port comparison for non-managed listeners or if original address doesn't match
	// Get the listener's address
	listenerAddr := listener.Addr()
	if listenerAddr == nil {
		return false
	}

	// Compare network (tcp) and address (host:port)
	// Note: The address from listener might include resolved host info like [::]:8080
	// while config address might be just :8080, so we need to extract the port
	var listenerPort, configPort string
	var err error

	_, listenerPort, err = net.SplitHostPort(listenerAddr.String())
	if err != nil || listenerPort == "" {
		return false
	}

	_, configPort, err = net.SplitHostPort(addr)
	if err != nil || configPort == "" {
		return false
	}

	// If ports don't match, we can't reuse
	if listenerPort != configPort {
		return false
	}

	return true
}

// getListener returns an existing listener if valid and reusable, or creates a new one
func (r *Runner) getListener(cfg *Config) (net.Listener, error) {
	// Check if we should try to reuse the existing listener
	if cfg.ReuseListener && isListenerValid(r.listener, cfg.ListenAddr) {
		r.logger.Debug("Reusing existing listener", "addr", r.listener.Addr().String())
		return r.listener, nil
	}

	// Close the existing listener if we have one but won't reuse it
	if r.listener != nil {
		r.logger.Debug("Closing existing listener")
		if err := r.listener.Close(); err != nil {
			// Log the error but continue - we'll try to create a new listener anyway
			r.logger.Warn("Error closing existing listener", "error", err)
			// Some errors (like listener already closed) aren't critical, but others might indicate issues
			// that could prevent a new listener from being created on the same address
		}
		r.listener = nil
	}

	// Create a new listener
	r.logger.Debug("Creating new listener", "addr", cfg.ListenAddr)

	// Create the listener with appropriate options
	lc := net.ListenConfig{}
	if cfg.TCPKeepAlive > 0 {
		lc.KeepAlive = cfg.TCPKeepAlive
	}

	baseListener, err := lc.Listen(r.ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	// Wrap the base listener with our managed listener
	listener := newManagedListener(baseListener, cfg.ListenAddr)

	r.logger.Debug("Created new listener",
		"requestedAddr", cfg.ListenAddr,
		"actualAddr", listener.Addr().String())

	return listener, nil
}
