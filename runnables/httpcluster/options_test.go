package httpcluster

import (
	"log/slog"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithLogger(t *testing.T) {
	t.Parallel()

	logger := slog.Default().WithGroup("test")
	runner, err := NewRunner(WithLogger(logger))
	require.NoError(t, err)

	assert.Equal(t, logger, runner.logger)
}

func TestWithLogHandler(t *testing.T) {
	t.Parallel()

	handler := slog.Default().Handler()
	runner, err := NewRunner(WithLogHandler(handler))
	require.NoError(t, err)

	// Logger should be created with the provided handler
	assert.NotNil(t, runner.logger)
}

func TestWithCustomSiphonChannel(t *testing.T) {
	t.Parallel()

	customChannel := make(chan map[string]*httpserver.Config, 5)
	runner, err := NewRunner(WithCustomSiphonChannel(customChannel))
	require.NoError(t, err)

	assert.Equal(t, customChannel, runner.configSiphon)

	// Verify the channel capacity
	assert.Equal(t, 5, cap(runner.configSiphon))
}

func TestWithStateChanBufferSize(t *testing.T) {
	t.Parallel()

	t.Run("valid buffer size", func(t *testing.T) {
		runner, err := NewRunner(WithStateChanBufferSize(20))
		require.NoError(t, err)

		assert.Equal(t, 20, runner.stateChanBufferSize)
	})

	t.Run("zero buffer size", func(t *testing.T) {
		runner, err := NewRunner(WithStateChanBufferSize(0))
		require.NoError(t, err)

		assert.Equal(t, 0, runner.stateChanBufferSize)
	})

	t.Run("negative buffer size returns error", func(t *testing.T) {
		_, err := NewRunner(WithStateChanBufferSize(-1))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "state channel buffer size cannot be negative")
		assert.Contains(t, err.Error(), "-1")
	})
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Parallel()

	t.Run("default is 5 seconds", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)
		assert.Equal(t, defaultShutdownTimeout, runner.shutdownTimeout)
	})

	t.Run("positive value is honored", func(t *testing.T) {
		runner, err := NewRunner(WithShutdownTimeout(2 * time.Second))
		require.NoError(t, err)
		assert.Equal(t, 2*time.Second, runner.shutdownTimeout)
	})

	t.Run("zero disables the bound", func(t *testing.T) {
		runner, err := NewRunner(WithShutdownTimeout(0))
		require.NoError(t, err)
		assert.Equal(t, time.Duration(0), runner.shutdownTimeout)
	})

	t.Run("negative value returns error", func(t *testing.T) {
		_, err := NewRunner(WithShutdownTimeout(-1 * time.Second))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "shutdown timeout cannot be negative")
	})
}

func TestOptionApplicationOrder(t *testing.T) {
	t.Parallel()

	// Test that multiple options are applied correctly
	logger := slog.Default().WithGroup("test")

	runner, err := NewRunner(
		WithLogger(logger),
		WithStateChanBufferSize(15),
		WithSiphonBuffer(3),
	)
	require.NoError(t, err)

	assert.Equal(t, logger, runner.logger)
	assert.Equal(t, 15, runner.stateChanBufferSize)
	assert.Equal(t, 3, cap(runner.configSiphon))
}

func TestOptionError(t *testing.T) {
	t.Parallel()

	// Test that an option error is properly propagated
	errorOption := func(r *Runner) error {
		return assert.AnError // Use testify's standard error
	}

	_, err := NewRunner(errorOption)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to apply option")
}

// TestRestartSupervisionDefaults verifies NewRunner installs the documented
// crash-supervision defaults, and that the options override them.
func TestRestartSupervisionDefaults(t *testing.T) {
	t.Parallel()

	r, err := NewRunner()
	require.NoError(t, err)
	assert.Equal(t, defaultRestartBackoffInitial, r.restartBackoffInitial)
	assert.Equal(t, defaultRestartBackoffMax, r.restartBackoffMax)
	assert.Equal(t, defaultMaxRestarts, r.maxRestarts)
	assert.Equal(t, defaultRestartWindow, r.restartWindow)

	r, err = NewRunner(
		WithRestartBackoff(250*time.Millisecond, 10*time.Second),
		WithMaxRestarts(7),
		WithRestartWindow(2*time.Minute),
	)
	require.NoError(t, err)
	assert.Equal(t, 250*time.Millisecond, r.restartBackoffInitial)
	assert.Equal(t, 10*time.Second, r.restartBackoffMax)
	assert.Equal(t, 7, r.maxRestarts)
	assert.Equal(t, 2*time.Minute, r.restartWindow)
}
