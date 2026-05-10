package supervisor

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestBroadcastState_SlowGetStateDoesNotBlockUnsubscribe verifies the
// architectural fix that broadcastState computes its state snapshot OUTSIDE
// subscriberMutex. With the snapshot inside the mutex, a slow GetState()
// implementation would stall every subscribe/unsubscribe operation; with the
// fix, unsubscribe proceeds while the broadcaster is parked in GetState.
//
// The test pins a broadcastState goroutine inside its GetStateMap call (via
// a Stateable whose GetState blocks until release), then calls the
// unsubscribe callback and asserts it returns before release fires. synctest
// makes the timing deterministic.
func TestBroadcastState_SlowGetStateDoesNotBlockUnsubscribe(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// blockNow gates the slow path on GetState. AddStateSubscriber's
		// own GetStateMap call must NOT block, so we flip the gate after
		// the subscriber registers.
		var blockNow atomic.Bool
		release := make(chan struct{})

		s := mocks.NewMockRunnableWithStateable()
		s.On("String").Return("svc").Maybe()
		s.On("GetStateChan", mock.Anything).Return(make(chan string, 1)).Maybe()
		s.On("GetState").Return("running").Run(func(args mock.Arguments) {
			if blockNow.Load() {
				<-release
			}
		}).Maybe()

		pid0, err := New(WithContext(t.Context()), WithRunnables(s))
		require.NoError(t, err)

		// Register the subscriber while GetState is fast.
		ch := make(chan StateMap, 5)
		unsubscribe := pid0.AddStateSubscriber(ch)

		// Make subsequent GetState calls block.
		blockNow.Store(true)

		// Spawn a broadcaster — it will compute the snapshot via
		// GetStateMap, which calls GetState, which blocks on release.
		broadcasterDone := make(chan struct{})
		go func() {
			pid0.broadcastState()
			close(broadcasterDone)
		}()

		// Wait until the broadcaster is durably parked in GetState.
		synctest.Wait()

		// Sanity check: broadcaster has NOT completed.
		select {
		case <-broadcasterDone:
			t.Fatal("broadcaster completed before release; slow GetState did not block")
		default:
		}

		// Now call unsubscribe. With the fix, broadcastState does not hold
		// subscriberMutex during GetStateMap, so this should return without
		// waiting for the broadcaster. Run it in a goroutine so synctest
		// can observe whether it durably blocks.
		unsubscribeDone := make(chan struct{})
		go func() {
			unsubscribe()
			close(unsubscribeDone)
		}()
		synctest.Wait()

		select {
		case <-unsubscribeDone:
			// Pass — unsubscribe completed despite the broadcaster being
			// blocked in its (mutex-free) snapshot computation.
		default:
			t.Fatal("unsubscribe blocked while broadcaster was in GetStateMap; " +
				"snapshot must be computed outside subscriberMutex")
		}

		// Drain the bubble: release the slow GetState so the broadcaster
		// can finish before the synctest function returns.
		close(release)
		<-broadcasterDone
	})
}

// TestStateConcurrency_StressBroadcastsSubscribesGetStateMap exercises
// concurrent broadcastState, AddStateSubscriber/unsubscribe, and GetStateMap
// callers against a single PIDZero. There are no correctness assertions
// beyond completion — the test exists to surface any data races caught by
// `go test -race` across the new mutex split in broadcastState and the
// shared p.stateSubscribers map.
func TestStateConcurrency_StressBroadcastsSubscribesGetStateMap(t *testing.T) {
	t.Parallel()

	const (
		goroutinesPerKind = 8
		iterations        = 200
	)

	a := mocks.NewMockRunnableWithStateable()
	a.On("String").Return("a").Maybe()
	a.On("GetStateChan", mock.Anything).Return(make(chan string, 1)).Maybe()
	a.On("GetState").Return("running").Maybe()

	b := mocks.NewMockRunnableWithStateable()
	b.On("String").Return("b").Maybe()
	b.On("GetStateChan", mock.Anything).Return(make(chan string, 1)).Maybe()
	b.On("GetState").Return("stopped").Maybe()

	pid0, err := New(WithRunnables(a, b))
	require.NoError(t, err)

	var wg sync.WaitGroup

	// Broadcasters: hammer broadcastState.
	for range goroutinesPerKind {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				pid0.broadcastState()
			}
		}()
	}

	// Subscriber churn: AddStateSubscriber + unsubscribe.
	for range goroutinesPerKind {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				ch := make(chan StateMap, 4)
				unsub := pid0.AddStateSubscriber(ch)
				unsub()
			}
		}()
	}

	// GetStateMap pollers.
	for range goroutinesPerKind {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				_ = pid0.GetStateMap()
			}
		}()
	}

	wg.Wait()
}

// TestGetStateMap_ReflectsLiveGetStateValues asserts the live-read contract:
// each GetStateMap call queries the runnables fresh, so subsequent calls
// reflect updated state without any caching layer between them.
func TestGetStateMap_ReflectsLiveGetStateValues(t *testing.T) {
	t.Parallel()

	s := mocks.NewMockRunnableWithStateable()
	s.On("String").Return("svc").Maybe()
	s.On("GetStateChan", mock.Anything).Return(make(chan string, 1)).Maybe()
	s.On("GetState").Return("starting").Once()
	s.On("GetState").Return("running").Once()
	s.On("GetState").Return("stopped").Once()

	pid0, err := New(WithRunnables(s))
	require.NoError(t, err)

	assert.Equal(t, StateMap{"svc": "starting"}, pid0.GetStateMap())
	assert.Equal(t, StateMap{"svc": "running"}, pid0.GetStateMap())
	assert.Equal(t, StateMap{"svc": "stopped"}, pid0.GetStateMap())

	s.AssertExpectations(t)
}

// TestBroadcastState_NoSubscribersFastPath verifies the perf optimization
// that broadcastState skips the GetStateMap snapshot when no subscribers
// are registered. The mock's GetState expectation has zero matching calls,
// so any invocation would fail AssertExpectations.
func TestBroadcastState_NoSubscribersFastPath(t *testing.T) {
	t.Parallel()

	s := mocks.NewMockRunnableWithStateable()
	s.On("String").Return("svc").Maybe()
	s.On("GetStateChan", mock.Anything).Return(make(chan string, 1)).Maybe()
	// GetState intentionally NOT expected — broadcastState must short-circuit
	// before computing the snapshot when the subscriber list is empty.

	pid0, err := New(WithRunnables(s))
	require.NoError(t, err)

	// No AddStateSubscriber calls. broadcastState should return immediately.
	pid0.broadcastState()
	pid0.broadcastState()
	pid0.broadcastState()

	s.AssertExpectations(t)
	s.AssertNotCalled(t, "GetState")
}
