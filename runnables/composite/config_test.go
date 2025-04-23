package composite

import (
	"testing"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		configName  string
		entries     []RunnableEntry[*mocks.Runnable]
		expectError bool
	}{
		{
			name:       "valid config",
			configName: "test-config",
			entries: []RunnableEntry[*mocks.Runnable]{
				{
					Runnable: &mocks.Runnable{},
					Config:   map[string]string{"key": "value"},
				},
			},
			expectError: false,
		},
		{
			name:        "empty name",
			configName:  "",
			entries:     []RunnableEntry[*mocks.Runnable]{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable for parallel execution
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := NewConfig(tt.configName, tt.entries)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, cfg)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, cfg)
				assert.Equal(t, tt.configName, cfg.Name)
				assert.Equal(t, tt.entries, cfg.Entries)
			}
		})
	}
}

func TestNewConfigFromRunnables(t *testing.T) {
	t.Parallel()

	mockRunnable1 := &mocks.Runnable{}
	mockRunnable2 := &mocks.Runnable{}
	sharedConfig := map[string]string{"key": "value"}

	cfg, err := NewConfigFromRunnables(
		"test",
		[]*mocks.Runnable{mockRunnable1, mockRunnable2},
		sharedConfig,
	)
	require.NoError(t, err)

	assert.Equal(t, "test", cfg.Name)
	assert.Len(t, cfg.Entries, 2)
	assert.Equal(t, mockRunnable1, cfg.Entries[0].Runnable)
	assert.Equal(t, mockRunnable2, cfg.Entries[1].Runnable)
	assert.Equal(t, sharedConfig, cfg.Entries[0].Config)
	assert.Equal(t, sharedConfig, cfg.Entries[1].Config)
}

func TestConfig_Equal(t *testing.T) {
	t.Parallel()

	mockRunnable1 := &mocks.Runnable{}
	mockRunnable1.On("String").Return("runnable1")

	mockRunnable2 := &mocks.Runnable{}
	mockRunnable2.On("String").Return("runnable2")

	mockRunnable3 := &mocks.Runnable{}
	mockRunnable3.On("String").Return("runnable1") // Intentionally same name as mockRunnable1

	// Use the same reference for configs that should be equal
	runtimeConfig1 := map[string]string{"key": "value"}

	// Use a different reference for configs that should be different
	runtimeConfig2 := map[string]string{"key": "different"}

	// Create entries
	entries1 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: runtimeConfig1},
		{Runnable: mockRunnable2, Config: runtimeConfig1},
	}

	entries2 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: runtimeConfig1},
		{Runnable: mockRunnable2, Config: runtimeConfig1},
	}

	entries3 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable2, Config: runtimeConfig1},
		{Runnable: mockRunnable1, Config: runtimeConfig1},
	}

	entries4 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable3, Config: runtimeConfig1},
		{Runnable: mockRunnable2, Config: runtimeConfig1},
	}

	entries5 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: runtimeConfig2},
		{Runnable: mockRunnable2, Config: runtimeConfig1},
	}

	entries6 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: nil},
		{Runnable: mockRunnable2, Config: nil},
	}

	entries7 := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: nil},
		{Runnable: mockRunnable2, Config: nil},
	}

	cfg1, err := NewConfig("test", entries1)
	require.NoError(t, err)

	// Same config with same entries
	cfg2, err := NewConfig("test", entries2)
	require.NoError(t, err)

	// Different name
	cfg3, err := NewConfig("different", entries1)
	require.NoError(t, err)

	// Different runnables (order)
	cfg4, err := NewConfig("test", entries3)
	require.NoError(t, err)

	// Different runnables (same string rep)
	cfg5, err := NewConfig("test", entries4)
	require.NoError(t, err)

	// Different config for one runnable
	cfg6, err := NewConfig("test", entries5)
	require.NoError(t, err)

	// Nil configs
	cfg7, err := NewConfig("test", entries6)
	require.NoError(t, err)

	// Another nil configs
	cfg8, err := NewConfig("test", entries7)
	require.NoError(t, err)

	assert.True(t, cfg1.Equal(cfg2), "identical configs should be equal")
	assert.False(t, cfg1.Equal(cfg3), "configs with different names should not be equal")
	assert.False(
		t,
		cfg1.Equal(cfg4),
		"configs with runnables in different order should not be equal",
	)
	assert.True(t, cfg1.Equal(cfg5), "configs with runnables with same String() should be equal")
	assert.False(t, cfg1.Equal(cfg6), "configs with different runnable configs should not be equal")
	assert.False(
		t,
		cfg1.Equal(cfg7),
		"config with runnable configs should not equal config with nil configs",
	)
	assert.True(t, cfg7.Equal(cfg8), "configs with nil runnable configs should be equal")
}

func TestConfig_String(t *testing.T) {
	t.Parallel()

	mockRunnable1 := &mocks.Runnable{}
	mockRunnable2 := &mocks.Runnable{}

	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: nil},
		{Runnable: mockRunnable2, Config: nil},
	}

	cfg, err := NewConfig("test-config", entries)
	require.NoError(t, err)

	str := cfg.String()
	assert.Contains(t, str, "test-config")
	assert.Contains(t, str, "2")
}
