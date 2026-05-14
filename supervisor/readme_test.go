// Package supervisor_test contains compile-checked mirrors of the code
// snippets in the top-level README.md. If you edit a snippet under
// "Testing" (or add a new one), update or add the matching Example
// function below so `go test` will catch typos and signature drift.
//
// Examples follow Go's godoc convention `Example_<suffix>`, where the
// suffix maps to the README subsection: `testingFakeRunnable` for the
// fake Runnable shown under "Testing". No Output comment is needed
// because we only care that the snippets compile against the current
// supervisor API.
package supervisor_test

import (
	"context"

	"github.com/robbyt/go-supervisor/supervisor"
)

// Mirrors the fakeRunnable definition shown in README.md's "Testing"
// section. Kept as an unexported type so it doesn't leak into godoc as
// a public API.
type readmeFakeRunnable struct {
	name    string
	started chan struct{}
	stopped chan struct{}
}

func (f *readmeFakeRunnable) String() string { return f.name }

func (f *readmeFakeRunnable) Run(ctx context.Context) error {
	close(f.started)
	<-ctx.Done()
	return nil
}

func (f *readmeFakeRunnable) Stop() { close(f.stopped) }

// Interface guard mirroring the README's promise that the fake
// satisfies supervisor.Runnable.
var _ supervisor.Runnable = (*readmeFakeRunnable)(nil)

func Example_testingFakeRunnable() {
	r := &readmeFakeRunnable{
		name:    "fake",
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	_, _ = supervisor.New(supervisor.WithRunnables(r))
}
