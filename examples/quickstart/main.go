// Quickstart example mirrors the "Quick Start" code block in the
// top-level README.md. Kept in sync so `go build ./examples/...`
// catches signature drift between the docs and the supervisor API.
// See PRs #128, #129 for the same pattern applied to other README
// snippets via readme_test.go.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/robbyt/go-supervisor/supervisor"
)

// Example service that implements Runnable interface
type MyService struct {
	name string
}

// Interface guard, ensuring that MyService implements Runnable
var _ supervisor.Runnable = (*MyService)(nil)

func (s *MyService) Run(ctx context.Context) error {
	fmt.Printf("%s: Starting\n", s.name)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("%s: Context canceled\n", s.name)
			return nil
		case <-ticker.C:
			fmt.Printf("%s: Tick\n", s.name)
		}
	}
}

func (s *MyService) Stop() {
	fmt.Printf("%s: Stopping\n", s.name)
	// Perform cleanup if needed
}

func (s *MyService) String() string {
	return s.name
}

func main() {
	// Create some services
	service1 := &MyService{name: "Service1"}
	service2 := &MyService{name: "Service2"}

	// Create a custom logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	// Create a supervisor with our services and custom logger
	super, err := supervisor.New(
		supervisor.WithRunnables(service1, service2),
		supervisor.WithLogHandler(handler),
	)
	if err != nil {
		fmt.Printf("Error creating supervisor: %v\n", err)
		os.Exit(1)
	}

	// Blocking call to Run(), starts listening to signals and starts all Runnables
	if err := super.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
