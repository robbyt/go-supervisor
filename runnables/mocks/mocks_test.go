/*
Package mocks_test provides tests to ensure mocks correctly implement all supervisor interfaces.
*/
package mocks_test

import (
	"testing"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
)

// TestInterfaceGuards uses compile-time type assertions to ensure that mock implementations
// correctly implement their respective interfaces.
func TestInterfaceGuards(t *testing.T) {
	// Create mock instances
	mockRunnable := mocks.NewMockRunnable()
	mockRunnableWithReload := mocks.NewMockRunnableWithReload()

	// Type assertions to verify mock types implement required interfaces
	var (
		// Basic Runnable should implement all three base interfaces
		_ supervisor.Runnable   = mockRunnable
		_ supervisor.Reloadable = mockRunnable
		_ supervisor.Stateable  = mockRunnable

		// MockRunnableWithReload should implement all interfaces including ReloadSender
		_ supervisor.Runnable     = mockRunnableWithReload
		_ supervisor.Reloadable   = mockRunnableWithReload
		_ supervisor.Stateable    = mockRunnableWithReload
		_ supervisor.ReloadSender = mockRunnableWithReload
	)

	// This test has no runtime assertions - it just needs to compile successfully.
	// If the mocks don't properly implement the interfaces, this test will fail to compile.
}
