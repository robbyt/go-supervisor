package httpserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootConfigCreateFailure(t *testing.T) {
	t.Parallel()

	callback := func() (*Config, error) {
		return &Config{
			ListenAddr:    ":8000",
			Routes:        Routes{},
			DrainTimeout:  1 * time.Second,
			ServerCreator: DefaultServerCreator,
		}, nil
	}

	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	err = runner.boot()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCreateConfig)
}
