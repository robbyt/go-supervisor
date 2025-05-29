package networking

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// reduce the chance of port conflicts
var (
	portMutex = &sync.Mutex{}
	usedPorts = make(map[int]struct{})
)

// GetRandomPort finds an available port for a test by binding to port 0
func GetRandomPort(tb testing.TB) int {
	tb.Helper()
	portMutex.Lock()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		portMutex.Unlock()
		tb.Fatalf("Failed to get random port: %v", err)
	}

	err = listener.Close()
	if err != nil {
		portMutex.Unlock()
		tb.Fatalf("Failed to close listener: %v", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	p := addr.Port
	// Check if the port is already used
	if _, ok := usedPorts[p]; ok {
		portMutex.Unlock()
		return GetRandomPort(tb)
	}
	usedPorts[p] = struct{}{}
	portMutex.Unlock()
	return p
}

// GetRandomListeningPort finds an available port for a test by binding to port 0, and returns a string like localhost:PORT
func GetRandomListeningPort(tb testing.TB) string {
	tb.Helper()
	p := GetRandomPort(tb)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err != nil {
		return GetRandomListeningPort(tb)
	}
	err = listener.Close()
	if err != nil {
		tb.Fatalf("Failed to close listener: %v", err)
	}

	return fmt.Sprintf("localhost:%d", p)
}
