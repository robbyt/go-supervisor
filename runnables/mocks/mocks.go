/*
Package mocks provides mock implementations of all supervisor interfaces for testing.
These mocks implement the Runnable, Reloadable, Stateable, and ReloadSender interfaces
following the canonical testify/mock pattern. Tests that need delayed return values
should use Call.After(d) per-expectation instead of mock-instance fields.

Example:
```go
import (

	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/robbyt/go-supervisor"
	"github.com/robbyt/go-supervisor/runnables/mocks"

)

	func TestMyComponent(t *testing.T) {
		// Create a mock service
		mockRunnable := mocks.NewMockRunnable()

		// Set expectations (use .After(d) for delayed returns)
		mockRunnable.On("Run", mock.Anything).Return(nil)
		mockRunnable.On("Stop").Once().After(100*time.Millisecond)

		// For state-based tests
		stateCh := make(chan string)
		mockRunnable.On("GetStateChan", mock.Anything).Return(stateCh)

		// Create supervisor with mock
		super := supervisor.New([]supervisor.Runnable{mockRunnable})

		// Run test...

		// Verify expectations
		mockRunnable.AssertExpectations(t)
	}

```
*/
package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// Runnable is a mock implementation of the Runnable, Reloadable, and Stateable interfaces
// using testify/mock.
type Runnable struct {
	mock.Mock
}

// NewMockRunnable creates a new Runnable mock.
func NewMockRunnable() *Runnable {
	return &Runnable{}
}

// Run mocks the Run method of the Runnable interface.
func (m *Runnable) Run(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// Stop mocks the Stop method of the Runnable interface.
func (m *Runnable) Stop() {
	m.Called()
}

// Reload mocks the Reload method of the Reloadable interface.
func (m *Runnable) Reload(ctx context.Context) {
	m.Called(ctx)
}

// String returns a string representation of the mock service.
// It can be mocked by doing mock.On("String").Return("customValue") in tests.
func (m *Runnable) String() string {
	if mock := m.Called(); mock.Get(0) != nil {
		return mock.String(0)
	}
	return "Runnable"
}

// MockRunnableWithStateable extends Runnable to also implement the Stateable interface.
type MockRunnableWithStateable struct {
	*Runnable
}

// GetState mocks the GetState method of the Stateable interface.
// It returns the current state of the service as configured in test expectations.
func (m *MockRunnableWithStateable) GetState() string {
	args := m.Called()
	return args.String(0)
}

// GetStateChan mocks the GetStateChan method of the Stateable interface.
// It returns a receive-only channel that will emit state updates as configured in test expectations.
func (m *MockRunnableWithStateable) GetStateChan(ctx context.Context) <-chan string {
	args := m.Called(ctx)
	return args.Get(0).(chan string)
}

// IsRunning mocks the IsRunning method of the Stateable interface.
// It returns true if the service is currently running, as configured in test expectations.
func (m *MockRunnableWithStateable) IsRunning() bool {
	args := m.Called()
	return args.Bool(0)
}

// NewMockRunnableWithStateable creates a new MockRunnableWithStateable.
func NewMockRunnableWithStateable() *MockRunnableWithStateable {
	return &MockRunnableWithStateable{
		Runnable: NewMockRunnable(),
	}
}

// MockRunnableWithReloadSender extends Runnable to also implement the ReloadSender interface.
type MockRunnableWithReloadSender struct {
	*Runnable
}

// GetReloadTrigger implements the ReloadSender interface.
// It returns a receive-only channel that emits signals when a reload is requested.
func (m *MockRunnableWithReloadSender) GetReloadTrigger() <-chan struct{} {
	args := m.Called()
	return args.Get(0).(chan struct{})
}

// NewMockRunnableWithReloadSender creates a new MockRunnableWithReload.
func NewMockRunnableWithReloadSender() *MockRunnableWithReloadSender {
	return &MockRunnableWithReloadSender{
		Runnable: NewMockRunnable(),
	}
}

// MockRunnableWithShutdownSender extends Runnable to also implement the ShutdownSender interface.
type MockRunnableWithShutdownSender struct {
	*Runnable
}

// ShutdownSender mocks implementation of the ShutdownSender interface.
// It returns a receive-only channel that emits signals when a shutdown is requested.
func (m *MockRunnableWithShutdownSender) GetShutdownTrigger() <-chan struct{} {
	args := m.Called()
	return args.Get(0).(chan struct{})
}

// NewMockRunnableWithShutdown creates a new MockRunnableWithReload.
func NewMockRunnableWithShutdownSender() *MockRunnableWithShutdownSender {
	return &MockRunnableWithShutdownSender{
		Runnable: NewMockRunnable(),
	}
}
