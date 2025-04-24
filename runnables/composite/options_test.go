package composite

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock runnable implementation for testing
type mockRunnable struct{}

// Required method implementations to satisfy the runnable interface constraint
func (m *mockRunnable) Run(ctx context.Context) error { return nil }
func (m *mockRunnable) Stop()                         {}
func (m *mockRunnable) String() string                { return "mockRunnable" }

type contextKeyType string

const testContextKey contextKeyType = "testKey"

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

func TestWithContext(t *testing.T) {
	t.Parallel()

	originalCtx := context.Background()
	runner := &Runner[*mockRunnable]{
		parentCtx: nil,
		cancel:    nil,
	}

	WithContext[*mockRunnable](originalCtx)(runner)

	// Verify the context and cancel function are set
	require.NotNil(t, runner.parentCtx, "Context should be set")
	require.NotNil(t, runner.cancel, "Cancel function should be set")

	// Test that the contexts are related (child can access parent values)
	originalCtx = context.WithValue(context.Background(), testContextKey, "value")
	runner = &Runner[*mockRunnable]{}
	WithContext[*mockRunnable](originalCtx)(runner)

	assert.Equal(t,
		"value", runner.parentCtx.Value(testContextKey),
		"Child context should inherit values from parent",
	)

	// Test with empty context - we should get a new cancellable context
	// but still be able to verify it's connected to the background context
	runner = &Runner[*mockRunnable]{
		parentCtx: nil,
		cancel:    nil,
	}
	// Use context.Background() instead of nil
	WithContext[*mockRunnable](context.Background())(runner)

	// Instead of checking for equality, verify:
	// 1. The context is not nil
	// 2. The cancel function is not nil
	// 3. The context is derived from Background() (it will be a cancel context)
	require.NotNil(t, runner.parentCtx, "Context should be set with Background()")
	require.NotNil(t, runner.cancel, "Cancel function should be set with Background()")

	// Verify it's a cancel context by calling the cancel function and checking if Done channel closes
	runner.cancel()
	select {
	case <-runner.parentCtx.Done():
		// This is what we want - the context was canceled
	default:
		t.Error("Context should be cancellable when created with Background()")
	}
}

func TestWithConfigCallback(t *testing.T) {
	t.Parallel()

	// Create a test callback function
	testCallback := func() (*Config[*mockRunnable], error) {
		return &Config[*mockRunnable]{Name: "test"}, nil
	}

	// Create a runner
	runner := &Runner[*mockRunnable]{
		configCallback: nil,
	}

	// Apply the option
	WithConfigCallback(testCallback)(runner)

	// Verify the callback was set
	require.NotNil(t, runner.configCallback, "Config callback should be set")

	// Test that the callback works and returns the expected value
	config, err := runner.configCallback()
	assert.NoError(t, err)
	assert.Equal(t, "test", config.Name, "Config callback should return the expected value")
}
