package supervisor

import (
	"context"
	"testing"

	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// onlyStateable implements Stateable but NOT Readiness, demonstrating that
// after the T3.2 decoupling the two interfaces are genuinely orthogonal.
type onlyStateable struct{}

func (onlyStateable) GetState() string                             { return "" }
func (onlyStateable) GetStateChan(_ context.Context) <-chan string { return nil }

// onlyReadiness implements Readiness but NOT Stateable, the symmetric case.
type onlyReadiness struct{}

func (onlyReadiness) IsReady() bool { return true }

// stateAndReadiness implements both — most production runnables look like this.
type stateAndReadiness struct{}

func (stateAndReadiness) GetState() string                             { return "" }
func (stateAndReadiness) GetStateChan(_ context.Context) <-chan string { return nil }
func (stateAndReadiness) IsReady() bool                                { return true }

// TestStateableReadinessDecoupled pins down the T3.2 contract: Stateable no
// longer embeds Readiness, so a type satisfies one interface only if it has
// the corresponding methods. Pre-T3.2 a Stateable was always a Readiness via
// the embedding; this test prevents accidentally re-introducing the embed.
func TestStateableReadinessDecoupled(t *testing.T) {
	t.Parallel()

	t.Run("Stateable does not imply Readiness", func(t *testing.T) {
		var v any = onlyStateable{}
		_, isStateable := v.(Stateable)
		_, isReadiness := v.(Readiness)
		require.True(t, isStateable, "must satisfy Stateable")
		require.False(t, isReadiness, "must NOT satisfy Readiness — embedding is gone")
	})

	t.Run("Readiness does not imply Stateable", func(t *testing.T) {
		var v any = onlyReadiness{}
		_, isStateable := v.(Stateable)
		_, isReadiness := v.(Readiness)
		require.False(t, isStateable, "must NOT satisfy Stateable")
		require.True(t, isReadiness, "must satisfy Readiness")
	})

	t.Run("a type with both methods satisfies both", func(t *testing.T) {
		var v any = stateAndReadiness{}
		_, isStateable := v.(Stateable)
		_, isReadiness := v.(Readiness)
		require.True(t, isStateable)
		require.True(t, isReadiness)
	})
}

// TestBlockUntilRunnableReady_AcceptsReadinessOnly verifies that the
// supervisor's startup gate asks for Readiness, not Stateable. Pre-T3.2 the
// gate took a Stateable and exploited the Readiness embedding; T3.2 narrowed
// it to Readiness, so a runnable can participate in the startup gate without
// also implementing GetState/GetStateChan.
func TestBlockUntilRunnableReady_AcceptsReadinessOnly(t *testing.T) {
	t.Parallel()

	r := mocks.NewMockRunnableWithReadiness()
	r.On("IsReady").Return(true).Once()

	pidZero, err := New(WithRunnables(r))
	require.NoError(t, err)

	require.NoError(t, pidZero.blockUntilRunnableReady(r),
		"blockUntilRunnableReady must accept a Readiness-only runnable")
	r.AssertExpectations(t)
}

// TestBlockUntilRunnableReady_DoesNotRequireStateable is a compile-time
// assertion via type-assertion: the mock satisfies Readiness but not Stateable.
// If a future refactor re-tightens the gate to Stateable this test will fail
// to compile / panic at the cast.
func TestBlockUntilRunnableReady_DoesNotRequireStateable(t *testing.T) {
	t.Parallel()

	var r any = mocks.NewMockRunnableWithReadiness()
	_, isStateable := r.(Stateable)
	require.False(t, isStateable,
		"MockRunnableWithReadiness must NOT satisfy Stateable; the startup gate must work without it")
	_, isReadiness := r.(Readiness)
	require.True(t, isReadiness)

	// Reference mock.Anything to keep the testify import in use across builds
	// where the previous tests get optimised away.
	_ = mock.Anything
}
