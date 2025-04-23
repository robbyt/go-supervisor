package composite

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/robbyt/go-supervisor/supervisor"
)

// runnable is a local alias to ensure the sub-runnables implement the
// supervisor.Runnable interface.
type runnable interface {
	supervisor.Runnable
}

// RunnableEntry associates a runnable with its configuration
type RunnableEntry[T runnable] struct {
	// Runnable is the component to be managed
	Runnable T

	// Config holds the configuration data for this specific runnable
	Config any
}

// Config represents the configuration for a CompositeRunner
type Config[T runnable] struct {
	// Name is a human-readable identifier for this composite runner
	Name string

	// Entries is the list of runnables with their associated configurations
	Entries []RunnableEntry[T]
}

// NewConfig creates a new Config instance for a CompositeRunner
func NewConfig[T runnable](
	name string,
	entries []RunnableEntry[T],
) (*Config[T], error) {
	if name == "" {
		return nil, errors.New("name cannot be empty")
	}

	return &Config[T]{
		Name:    name,
		Entries: entries,
	}, nil
}

// NewConfigFromRunnables creates a Config from a list of runnables, all using the same config
func NewConfigFromRunnables[T runnable](
	name string,
	runnables []T,
	sharedConfig any,
) (*Config[T], error) {
	if name == "" {
		return nil, errors.New("name cannot be empty")
	}

	entries := make([]RunnableEntry[T], len(runnables))
	for i, runnable := range runnables {
		entries[i] = RunnableEntry[T]{
			Runnable: runnable,
			Config:   sharedConfig,
		}
	}

	return &Config[T]{
		Name:    name,
		Entries: entries,
	}, nil
}

// Equal compares two configs for equality
func (c *Config[T]) Equal(other *Config[T]) bool {
	if c.Name != other.Name {
		return false
	}

	if len(c.Entries) != len(other.Entries) {
		return false
	}

	// Compare runnables and their configs
	for i, entry := range c.Entries {
		// Compare runnable by string representation
		if entry.Runnable.String() != other.Entries[i].Runnable.String() {
			return false
		}

		// For config, use reflection for comparison
		if !reflect.DeepEqual(entry.Config, other.Entries[i].Config) {
			return false
		}
	}

	return true
}

// String returns a string representation of the Config
func (c *Config[T]) String() string {
	return fmt.Sprintf("Config{Name: %s, Entries: %d}", c.Name, len(c.Entries))
}
