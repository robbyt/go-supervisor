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
	mockRunnableWithState := mocks.NewMockRunnableWithStatable()
	mockRunnableWithReload := mocks.NewMockRunnableWithReloadSender()

	// Type assertions to verify mock types implement required interfaces
	var (
		// Basic Runnable should implement the base Runnable interface
		_ supervisor.Runnable   = mockRunnable
		_ supervisor.Reloadable = mockRunnable

		// MockRunnableWithState should implement the base Runnable interface + Stateable
		_ supervisor.Runnable   = mockRunnableWithState
		_ supervisor.Reloadable = mockRunnableWithState
		_ supervisor.Stateable  = mockRunnableWithState

		// MockRunnableWithReload should implement the base Runnable interface + ReloadSender
		_ supervisor.Runnable     = mockRunnableWithReload
		_ supervisor.Reloadable   = mockRunnableWithReload
		_ supervisor.ReloadSender = mockRunnableWithReload
	)

	// This test has no runtime assertions - it just needs to compile successfully.
	// If the mocks don't properly implement the interfaces, this test will fail to compile.
}
