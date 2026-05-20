// Package supervisor_test contains compile-checked mirrors of the code
// snippets in the top-level README.md. If you edit one of the mirrored
// snippets (or add a new one), update or add the matching Example
// function below so `go test` will catch typos and signature drift.
//
// Examples follow Go's godoc convention `Example_<suffix>`, where the
// suffix maps to the README subsection: `testingFakeRunnable` for the
// fake Runnable shown under "Testing", `reloadableConfigurable` for the
// `ConfigurableService` snippet under "Implementing a Reloadable
// Service". No Output comment is needed because we only care that the
// snippets compile against the current supervisor API.
package supervisor_test

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	if _, err := supervisor.New(supervisor.WithRunnables(r)); err != nil {
		panic(err)
	}
}

// readmeMyService mirrors the MyService struct defined in README.md's
// "Quick Start" — the Reloadable Service snippet extends this type so
// we need a local stand-in to compile-check the snippet here. The
// Quick Start itself lives in examples/quickstart/main.go.
type readmeMyService struct {
	name string
}

func (s *readmeMyService) String() string { return s.name }

func (s *readmeMyService) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (s *readmeMyService) Stop() {}

// readmeConfig mirrors the Config struct defined inline in the
// Reloadable Service snippet.
type readmeConfig struct {
	Interval time.Duration
}

// readmeLoadConfig stands in for the snippet's loadConfig() helper.
// The README leaves it undefined (a user-supplied loader); the stub
// just returns a non-nil config so Reload's success path compiles.
func readmeLoadConfig() (*readmeConfig, error) {
	return &readmeConfig{Interval: time.Second}, nil
}

// Mirrors the ConfigurableService struct from README.md's
// "Implementing a Reloadable Service" section. Kept unexported so it
// doesn't leak into godoc.
type readmeConfigurableService struct {
	readmeMyService
	config *readmeConfig
	mu     sync.Mutex
}

// Interface guards, ensuring that the snippet's compose still
// satisfies both Runnable and Reloadable.
var (
	_ supervisor.Runnable   = (*readmeConfigurableService)(nil)
	_ supervisor.Reloadable = (*readmeConfigurableService)(nil)
)

func (s *readmeConfigurableService) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load new config from file or environment
	newConfig, err := readmeLoadConfig()
	if err != nil {
		return err
	}
	s.config = newConfig

	fmt.Printf("%s: Configuration reloaded\n", s.name)
	return nil
}

func Example_reloadableConfigurable() {
	s := &readmeConfigurableService{
		readmeMyService: readmeMyService{name: "service"},
	}
	if _, err := supervisor.New(supervisor.WithRunnables(s)); err != nil {
		panic(err)
	}
}
