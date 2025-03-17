/*
The mocks package provides mock implementations of all interfaces for testing:

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
	    mockService := &mocks.MockService{}

	    // Set expectations
	    mockService.On("Run", mock.Anything).Return(nil)
	    mockService.On("Stop").Once()

	    // Create supervisor with mock
	    super := supervisor.New([]supervisor.Runnable{mockService})

	    // Run test...

	    // Verify expectations
	    mockService.AssertExpectations(t)
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

// MockService is a mock implementation of the Runnable, Reloadable, and Stateable interfaces
// using testify/mock. It allows for configurable delays in method responses to simulate
// service behavior.
type MockService struct {
	mock.Mock
	DelayRun      time.Duration // Delay before Run returns
	DelayStop     time.Duration // Delay before Stop returns
	DelayReload   time.Duration // Delay before Reload returns
	DelayGetState time.Duration // Delay before GetState and GetStateChan return
}

// NewMockService creates a new MockService with default delays.
func NewMockService() *MockService {
	return &MockService{
		DelayRun:      defaultDelay,
		DelayStop:     defaultDelay,
		DelayReload:   defaultDelay,
		DelayGetState: defaultDelay,
	}
}

// Run mocks the Run method of the Runnable interface.
// It sleeps for DelayRun duration before returning the mocked error result.
func (m *MockService) Run(ctx context.Context) error {
	time.Sleep(m.DelayRun)
	args := m.Called(ctx)
	return args.Error(0)
}

// Stop mocks the Stop method of the Runnable interface.
// It sleeps for DelayStop duration before recording the call.
func (m *MockService) Stop() {
	time.Sleep(m.DelayStop)
	m.Called()
}

// Reload mocks the Reload method of the Reloadable interface.
// It sleeps for DelayReload duration before recording the call.
func (m *MockService) Reload() {
	time.Sleep(m.DelayReload)
	m.Called()
}

// GetState mocks the GetState method of the Stateable interface.
// It returns the current state of the service as configured in test expectations.
func (m *MockService) GetState() string {
	time.Sleep(m.DelayGetState)
	args := m.Called()
	return args.String(0)
}

// GetStateChan mocks the GetStateChan method of the Stateable interface.
// It returns a receive-only channel that will emit state updates as configured in test expectations.
func (m *MockService) GetStateChan(ctx context.Context) <-chan string {
	time.Sleep(m.DelayGetState)
	args := m.Called(ctx)
	// Convert chan string to <-chan string if needed
	ch := args.Get(0).(chan string)
	return ch
}

// String returns a string representation of the mock service.
// It can be mocked by doing mock.On("String").Return("customValue") in tests.
func (m *MockService) String() string {
	if mock := m.Called(); mock.Get(0) != nil {
		return mock.String(0)
	}
	return "MockService"
}

// MockServiceWithReload extends MockService to also implement the ReloadSender interface.
type MockServiceWithReload struct {
	*MockService
}

// GetReloadTrigger implements the ReloadSender interface.
// It returns a receive-only channel that emits signals when a reload is requested.
func (m *MockServiceWithReload) GetReloadTrigger() <-chan struct{} {
	args := m.Called()
	return args.Get(0).(chan struct{})
}

// NewMockServiceWithReload creates a new MockServiceWithReload with default delays.
func NewMockServiceWithReload() *MockServiceWithReload {
	return &MockServiceWithReload{
		MockService: NewMockService(),
	}
}
