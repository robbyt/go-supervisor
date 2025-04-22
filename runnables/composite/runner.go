package composite

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// Option represents a functional option for configuring CompositeRunner
type Option[T RunnableType] func(*CompositeRunner[T])

// WithLogHandler sets a custom slog handler for the CompositeRunner instance.
func WithLogHandler[T RunnableType](handler slog.Handler) Option[T] {
	return func(c *CompositeRunner[T]) {
		if handler != nil {
			c.logger = slog.New(handler.WithGroup("composite.Runner"))
		}
	}
}

// WithContext sets a custom context for the CompositeRunner instance.
// This allows for more granular control over cancellation and timeouts.
func WithContext[T RunnableType](ctx context.Context) Option[T] {
	return func(c *CompositeRunner[T]) {
		if ctx != nil {
			c.ctx, c.cancel = context.WithCancel(ctx)
		}
	}
}

// WithConfigCallback sets the function that will be called to load or reload configuration.
// This option is required when creating a new CompositeRunner.
func WithConfigCallback[T RunnableType](callback func() (*Config[T], error)) Option[T] {
	return func(c *CompositeRunner[T]) {
		c.configCallback = callback
	}
}

// ReloadableWithConfig is an interface for runnables that can reload with specific config
type ReloadableWithConfig interface {
	supervisor.Reloadable
	ReloadWithConfig(config any)
}

// CompositeRunner implements a component that manages multiple runnables of the same type
// as a single unit. It satisfies the Runnable, Reloadable, and Stateable interfaces.
type CompositeRunner[T RunnableType] struct {
	config         atomic.Pointer[Config[T]]
	configCallback func() (*Config[T], error)
	runnablesMu    sync.RWMutex
	fsm            finitestate.Machine
	ctx            context.Context
	cancel         context.CancelFunc
	logger         *slog.Logger
	serverErrors   chan error
}

// Interface guards to ensure all required interfaces are implemented
var (
	_ supervisor.Runnable   = (*CompositeRunner[supervisor.Runnable])(nil)
	_ supervisor.Reloadable = (*CompositeRunner[supervisor.Runnable])(nil)
	_ supervisor.Stateable  = (*CompositeRunner[supervisor.Runnable])(nil)
)

// NewRunner creates a new CompositeRunner instance with the provided options.
func NewRunner[T RunnableType](opts ...Option[T]) (*CompositeRunner[T], error) {
	// Set default logger
	logger := slog.Default().WithGroup("composite.Runner")

	// Initialize with a background context by default
	ctx, cancel := context.WithCancel(context.Background())

	r := &CompositeRunner[T]{
		config:       atomic.Pointer[Config[T]]{},
		serverErrors: make(chan error, 1),
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	// Validate required options
	if r.configCallback == nil {
		return nil, fmt.Errorf(
			"%w: config callback is required (use WithConfigCallback)",
			ErrCompositeRunnable,
		)
	}

	// Create FSM with the configured logger
	fsmLogger := r.logger.WithGroup("fsm")
	machine, err := finitestate.New(fsmLogger.Handler())
	if err != nil {
		return nil, fmt.Errorf("unable to create fsm: %w", err)
	}
	r.fsm = machine

	// Load initial config
	if cfg := r.getConfig(); cfg == nil {
		return nil, fmt.Errorf("%w: failed to load initial config", ErrConfigMissing)
	}

	return r, nil
}

// String returns a string representation of the CompositeRunner instance.
func (r *CompositeRunner[T]) String() string {
	cfg := r.getConfig()
	if cfg == nil {
		return "CompositeRunner<nil>"
	}
	return fmt.Sprintf("CompositeRunner{name: %s, entries: %d}", cfg.Name, len(cfg.Entries))
}

// setConfig atomically updates the current configuration.
func (r *CompositeRunner[T]) setConfig(config *Config[T]) {
	r.config.Store(config)
	r.logger.Debug("Config updated", "config", config)
}

// getConfig returns the current configuration, loading it via the callback if necessary.
func (r *CompositeRunner[T]) getConfig() *Config[T] {
	config := r.config.Load()
	if config != nil {
		return config
	}

	r.logger.Debug("Loading new config via callback")
	newConfig, err := r.configCallback()
	if err != nil {
		r.logger.Error("Failed to load config", "error", err)
		return nil
	}

	if newConfig == nil {
		r.logger.Error("Config callback returned nil")
		return nil
	}

	r.setConfig(newConfig)
	return newConfig
}

// GetChildStates returns a map of child runnable names to their states.
func (r *CompositeRunner[T]) GetChildStates() map[string]string {
	r.runnablesMu.RLock()
	defer r.runnablesMu.RUnlock()

	states := make(map[string]string)
	cfg := r.getConfig()
	if cfg == nil {
		return states
	}

	for _, entry := range cfg.Entries {
		if s, ok := any(entry.Runnable).(supervisor.Stateable); ok {
			states[entry.Runnable.String()] = s.GetState()
		} else {
			states[entry.Runnable.String()] = "unknown"
		}
	}

	return states
}
