/*
Package mocks provides mock implementations of all supervisor interfaces for testing.
These mocks implement the Runnable, Reloadable, Stateable, and ReloadSender interfaces
with configurable delays to simulate real service behavior in tests.

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

		// Set expectations
		mockRunnable.On("Run", mock.Anything).Return(nil)
		mockRunnable.On("Stop").Once()

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
	"time"

	"github.com/stretchr/testify/mock"
)

const defaultDelay = 1 * time.Millisecond

// Runnable is a mock implementation of the Runnable, Reloadable, and Stateable interfaces
// using testify/mock. It allows for configurable delays in method responses to simulate
// service behavior.
type Runnable struct {
	mock.Mock
	DelayRun      time.Duration // Delay before Run returns
	DelayStop     time.Duration // Delay before Stop returns
	DelayReload   time.Duration // Delay before Reload returns
	DelayGetState time.Duration // Delay before GetState and GetStateChan return
}

// NewMockRunnable creates a new Runnable mock with default delays.
func NewMockRunnable() *Runnable {
	return &Runnable{
		DelayRun:      defaultDelay,
		DelayStop:     defaultDelay,
		DelayReload:   defaultDelay,
		DelayGetState: defaultDelay,
	}
}

// Run mocks the Run method of the Runnable interface.
// It sleeps for DelayRun duration before returning the mocked error result.
func (m *Runnable) Run(ctx context.Context) error {
	time.Sleep(m.DelayRun)
	args := m.Called(ctx)
	return args.Error(0)
}

// Stop mocks the Stop method of the Runnable interface.
// It sleeps for DelayStop duration before recording the call.
func (m *Runnable) Stop() {
	time.Sleep(m.DelayStop)
	m.Called()
}

// Reload mocks the Reload method of the Reloadable interface.
// It sleeps for DelayReload duration before recording the call.
func (m *Runnable) Reload() {
	time.Sleep(m.DelayReload)
	m.Called()
}

// GetState mocks the GetState method of the Stateable interface.
// It returns the current state of the service as configured in test expectations.
func (m *Runnable) GetState() string {
	time.Sleep(m.DelayGetState)
	args := m.Called()
	return args.String(0)
}

// GetStateChan mocks the GetStateChan method of the Stateable interface.
// It returns a receive-only channel that will emit state updates as configured in test expectations.
func (m *Runnable) GetStateChan(ctx context.Context) <-chan string {
	time.Sleep(m.DelayGetState)
	args := m.Called(ctx)
	// For testing purposes, most tests will pass a regular chan string
	// which is compatible with the expected <-chan string return type
	return args.Get(0).(chan string)
}

// String returns a string representation of the mock service.
// It can be mocked by doing mock.On("String").Return("customValue") in tests.
func (m *Runnable) String() string {
	if mock := m.Called(); mock.Get(0) != nil {
		return mock.String(0)
	}
	return "Runnable"
}

// MockRunnableWithReload extends Runnable to also implement the ReloadSender interface.
type MockRunnableWithReload struct {
	*Runnable
}

// GetReloadTrigger implements the ReloadSender interface.
// It returns a receive-only channel that emits signals when a reload is requested.
func (m *MockRunnableWithReload) GetReloadTrigger() <-chan struct{} {
	args := m.Called()
	// For testing purposes, most tests will pass a regular chan struct{}
	// which is compatible with the expected <-chan struct{} return type
	return args.Get(0).(chan struct{})
}

// NewMockRunnableWithReload creates a new MockRunnableWithReload with default delays.
func NewMockRunnableWithReload() *MockRunnableWithReload {
	return &MockRunnableWithReload{
		Runnable: NewMockRunnable(),
	}
}
