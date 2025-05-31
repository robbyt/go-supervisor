package httpcluster

import (
	"log/slog"
	"testing"

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
