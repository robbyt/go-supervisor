package networking

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// maxPortAttempts bounds the retry loop in GetRandomPort and
// GetRandomListeningPort so a pathological run cannot grow the stack
// (previously via unbounded recursion) or hang the test.
const maxPortAttempts = 100

// listenTCP wraps net.Listen so tests can inject bind failures.
var listenTCP = net.Listen

// reduce the chance of port conflicts
var (
	portMutex = &sync.Mutex{}
	usedPorts = make(map[int]struct{})
)

// GetRandomPort finds an available port for a test by binding to port 0.
// It retries up to maxPortAttempts times if the kernel hands back a port
// already taken from this process; on exhaustion it calls tb.Fatalf.
func GetRandomPort(tb testing.TB) int {
	tb.Helper()
	for attempt := 0; attempt < maxPortAttempts; attempt++ {
		portMutex.Lock()
		listener, err := listenTCP("tcp", ":0")
		if err != nil {
			portMutex.Unlock()
			tb.Fatalf("Failed to get random port: %v", err)
			return 0
		}

		if err := listener.Close(); err != nil {
			portMutex.Unlock()
			tb.Fatalf("Failed to close listener: %v", err)
			return 0
		}

		p := listener.Addr().(*net.TCPAddr).Port
		if _, taken := usedPorts[p]; !taken {
			usedPorts[p] = struct{}{}
			portMutex.Unlock()
			return p
		}
		portMutex.Unlock()
	}
	tb.Fatalf("Failed to find unused random port after %d attempts", maxPortAttempts)
	return 0
}

// GetRandomListeningPort finds an available port for a test by binding to port 0,
// and returns a string like localhost:PORT. It retries up to maxPortAttempts times
// if the chosen port can't be re-bound; on exhaustion it calls tb.Fatalf with the
// last underlying bind error so failures are diagnosable.
func GetRandomListeningPort(tb testing.TB) string {
	tb.Helper()
	var lastErr error
	for attempt := 0; attempt < maxPortAttempts; attempt++ {
		p := GetRandomPort(tb)
		listener, err := listenTCP("tcp", fmt.Sprintf(":%d", p))
		if err != nil {
			lastErr = err
			// Don't burn this port in usedPorts since we never actually bound it;
			// otherwise repeated failures could permanently exhaust the pool.
			portMutex.Lock()
			delete(usedPorts, p)
			portMutex.Unlock()
			continue
		}
		if err := listener.Close(); err != nil {
			tb.Fatalf("Failed to close listener: %v", err)
			return ""
		}
		return fmt.Sprintf("localhost:%d", p)
	}
	tb.Fatalf(
		"Failed to find listening port after %d attempts: last error: %v",
		maxPortAttempts,
		lastErr,
	)
	return ""
}
