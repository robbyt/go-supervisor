package networking

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRandomPort(t *testing.T) {
	t.Parallel()

	p := GetRandomPort(t)
	assert.Positive(t, p)
	assert.LessOrEqual(t, p, 65535)
}

func TestGetRandomPortReturnsUnique(t *testing.T) {
	t.Parallel()

	const n = 20
	seen := make(map[int]struct{}, n)
	for i := 0; i < n; i++ {
		p := GetRandomPort(t)
		_, dup := seen[p]
		assert.False(t, dup, "GetRandomPort returned duplicate port %d", p)
		seen[p] = struct{}{}
	}
}

func TestGetRandomListeningPort(t *testing.T) {
	t.Parallel()

	addr := GetRandomListeningPort(t)
	require.True(t, strings.HasPrefix(addr, "localhost:"))

	// The returned address must actually be bindable.
	l, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	require.NoError(t, l.Close())
}

// fakeTB captures Fatalf without aborting the surrounding test.
type fakeTB struct {
	testing.TB
	mu       sync.Mutex
	fatalMsg string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fatalMsg == "" {
		f.fatalMsg = fmt.Sprintf(format, args...)
	}
}

func (f *fakeTB) message() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fatalMsg
}

func TestGetRandomPortFailsAfterLimit(t *testing.T) {
	// Not parallel: this test mutates the package-level usedPorts map.
	portMutex.Lock()
	originalUsed := usedPorts
	usedPorts = make(map[int]struct{})
	for p := 1; p <= 65535; p++ {
		usedPorts[p] = struct{}{}
	}
	portMutex.Unlock()

	t.Cleanup(func() {
		portMutex.Lock()
		usedPorts = originalUsed
		portMutex.Unlock()
	})

	tb := &fakeTB{}
	got := GetRandomPort(tb)
	assert.Equal(t, 0, got)
	assert.Contains(t, tb.message(), fmt.Sprintf("%d attempts", maxPortAttempts))
}

// withListenTCP swaps the package-level listenTCP for the duration of a test.
// Not safe for parallel tests in this package.
func withListenTCP(t *testing.T, fn func(network, address string) (net.Listener, error)) {
	t.Helper()
	original := listenTCP
	listenTCP = fn
	t.Cleanup(func() { listenTCP = original })
}

func TestGetRandomPortFailsOnListenError(t *testing.T) {
	withListenTCP(t, func(network, address string) (net.Listener, error) {
		return nil, errors.New("simulated listen failure")
	})

	tb := &fakeTB{}
	got := GetRandomPort(tb)
	assert.Equal(t, 0, got)
	assert.Contains(t, tb.message(), "Failed to get random port")
	assert.Contains(t, tb.message(), "simulated listen failure")
}

func TestGetRandomListeningPortRetriesOnRebindFailure(t *testing.T) {
	var failedPort int

	withListenTCP(t, func(network, address string) (net.Listener, error) {
		// Calls alternate: GetRandomPort (":0") then re-bind on port (":N").
		// Fail the first re-bind, succeed afterwards.
		if address != ":0" && failedPort == 0 {
			// Capture the port so we can check it gets released, then fail.
			p, err := strconv.Atoi(strings.TrimPrefix(address, ":"))
			require.NoError(t, err)
			failedPort = p
			return nil, errors.New("simulated rebind failure")
		}
		return net.Listen(network, address)
	})

	addr := GetRandomListeningPort(t)
	require.True(t, strings.HasPrefix(addr, "localhost:"))

	// failedPort must NOT remain marked as used after a failed rebind, otherwise
	// repeated failures would exhaust the pool.
	require.NotZero(t, failedPort)
	portMutex.Lock()
	_, stillUsed := usedPorts[failedPort]
	portMutex.Unlock()
	assert.False(t, stillUsed, "port %d was burned in usedPorts after rebind failure", failedPort)
}

func TestGetRandomListeningPortFailsAfterLimit(t *testing.T) {
	withListenTCP(t, func(network, address string) (net.Listener, error) {
		// GetRandomPort uses ":0"; let it succeed so we exercise the
		// re-bind branch and the lastErr propagation.
		if address == ":0" {
			return net.Listen(network, address)
		}
		return nil, errors.New("simulated rebind failure")
	})

	tb := &fakeTB{}
	got := GetRandomListeningPort(tb)
	assert.Empty(t, got)
	msg := tb.message()
	assert.Contains(t, msg, fmt.Sprintf("%d attempts", maxPortAttempts))
	assert.Contains(t, msg, "simulated rebind failure")
}
