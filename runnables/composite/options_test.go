package composite

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Mock runnable implementation for testing
type mockRunnable struct{}

// Required method implementations to satisfy the runnable interface constraint
func (m *mockRunnable) Run(ctx context.Context) error { return nil }
func (m *mockRunnable) Stop()                         {}
func (m *mockRunnable) String() string                { return "mockRunnable" }

func TestWithLogHandler(t *testing.T) {
	t.Parallel()

	testHandler := slog.NewTextHandler(nil, nil)
	runner := &Runner[*mockRunnable]{
		logger: slog.Default(),
	}

	WithLogHandler[*mockRunnable](testHandler)(runner)
	assert.NotEqual(t, slog.Default(), runner.logger, "Logger should be changed")

	runner = &Runner[*mockRunnable]{
		logger: slog.Default(),
	}
	WithLogHandler[*mockRunnable](nil)(runner)
	assert.Equal(t, slog.Default(), runner.logger, "Logger should not change with nil handler")
}
