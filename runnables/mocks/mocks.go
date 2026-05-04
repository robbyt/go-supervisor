// Package mocks provides mock implementations of supervisor interfaces for
// testing: Runnable, Reloadable, Stateable, ReloadSender, and ShutdownSender.
// All mocks follow the canonical testify/mock pattern. Tests that need delayed
// return values should use Call.After(d) per-expectation rather than
// mock-instance fields.
//
// Example:
//
//	import (
//		"testing"
//		"time"
//
//		"github.com/stretchr/testify/mock"
//
//		"github.com/robbyt/go-supervisor/runnables/mocks"
//		"github.com/robbyt/go-supervisor/supervisor"
//	)
//
//	func TestMyComponent(t *testing.T) {
//		mockRunnable := mocks.NewMockRunnable()
//
//		mockRunnable.On("Run", mock.Anything).Return(nil)
//		mockRunnable.On("Stop").Return().After(100 * time.Millisecond)
//
//		super, err := supervisor.New(supervisor.WithRunnables(mockRunnable))
//		if err != nil {
//			t.Fatal(err)
//		}
//		_ = super // run test...
//
//		mockRunnable.AssertExpectations(t)
//	}
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

// IsReady mocks the IsReady method of the Readiness interface. It returns
// true once the service has finished its startup phase, as configured in
// test expectations. Stateable and Readiness are now orthogonal interfaces;
// MockRunnableWithStateable implements both for backwards compatibility with
// existing tests. Tests that want only Readiness (no Stateable) should use
// MockRunnableWithReadiness instead.
func (m *MockRunnableWithStateable) IsReady() bool {
	args := m.Called()
	return args.Bool(0)
}

// NewMockRunnableWithStateable creates a new MockRunnableWithStateable.
func NewMockRunnableWithStateable() *MockRunnableWithStateable {
	return &MockRunnableWithStateable{
		Runnable: NewMockRunnable(),
	}
}

// MockRunnableWithReadiness extends Runnable to implement only the Readiness
// interface (without Stateable's GetState/GetStateChan). Use this for tests
// that exercise the supervisor's startup gate without setting up state-channel
// monitoring expectations.
type MockRunnableWithReadiness struct {
	*Runnable
}

// IsReady mocks the IsReady method of the Readiness interface.
func (m *MockRunnableWithReadiness) IsReady() bool {
	args := m.Called()
	return args.Bool(0)
}

// NewMockRunnableWithReadiness creates a new MockRunnableWithReadiness.
func NewMockRunnableWithReadiness() *MockRunnableWithReadiness {
	return &MockRunnableWithReadiness{
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
