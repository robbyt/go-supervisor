package networking

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRandomPort(t *testing.T) {
	t.Parallel()

	p := GetRandomPort(t)
	assert.Greater(t, p, 0)
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
