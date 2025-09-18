package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseWriter(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	assert.NotNil(t, rw, "newResponseWriter should return non-nil ResponseWriter")
	assert.Equal(t, 0, rw.Status(), "initial status should be 0")
	assert.False(t, rw.Written(), "initial written state should be false")
	assert.Equal(t, 0, rw.Size(), "initial size should be 0")
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"201 Created", http.StatusCreated},
		{"400 Bad Request", http.StatusBadRequest},
		{"302 Found", http.StatusFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalWriter := httptest.NewRecorder()
			rw := newResponseWriter(originalWriter)

			assert.False(t, rw.Written(), "should not be written initially")
			assert.Equal(t, 0, rw.Status(), "initial status should be 0")

			rw.WriteHeader(tt.statusCode)

			assert.True(t, rw.Written(), "should be written after WriteHeader")
			assert.Equal(t, tt.statusCode, rw.Status(), "status should match written header")
			assert.Equal(t, 0, rw.Size(), "size should be 0 with no body written")
		})
	}
}

func TestResponseWriter_WriteHeader_MultipleCallsIgnored(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	rw.WriteHeader(http.StatusOK)
	assert.Equal(t, http.StatusOK, rw.Status(), "first WriteHeader should set status")
	assert.True(t, rw.Written(), "first WriteHeader should set written flag")

	rw.WriteHeader(http.StatusNotFound)
	assert.Equal(
		t,
		http.StatusOK,
		rw.Status(),
		"second WriteHeader should be ignored, status should remain 200",
	)
	assert.True(t, rw.Written(), "written flag should remain true")
}

func TestResponseWriter_Write(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	data := []byte("Hello, World!")

	assert.False(t, rw.Written(), "should not be written initially")
	assert.Equal(t, 0, rw.Size(), "initial size should be 0")

	n, err := rw.Write(data)

	require.NoError(t, err, "Write should not return error")
	assert.Equal(t, len(data), n, "Write should return number of bytes written")
	assert.Equal(t, len(data), rw.Size(), "Size should match bytes written")
	assert.True(t, rw.Written(), "Written flag should be true after Write")
	assert.Equal(
		t,
		http.StatusOK,
		rw.Status(),
		"Status should be 200 when Write called without explicit WriteHeader",
	)
}

func TestResponseWriter_Write_WithExplicitHeader(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	rw.WriteHeader(http.StatusCreated)

	data := []byte("Created!")
	n, err := rw.Write(data)

	require.NoError(t, err, "Write should not return error")
	assert.Equal(t, len(data), n, "Write should return number of bytes written")
	assert.Equal(t, len(data), rw.Size(), "Size should match bytes written")
	assert.True(t, rw.Written(), "Written flag should be true after Write")
	assert.Equal(
		t,
		http.StatusCreated,
		rw.Status(),
		"Status should remain as explicitly set status",
	)
}

func TestResponseWriter_Write_MultipleWrites(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	data1 := []byte("Hello, ")
	data2 := []byte("World!")
	data3 := []byte(" How are you?")

	n1, err1 := rw.Write(data1)
	require.NoError(t, err1, "first Write should not return error")
	assert.Equal(t, len(data1), n1, "first Write should return bytes written")
	assert.Equal(t, len(data1), rw.Size(), "size should match first write")
	assert.True(t, rw.Written(), "written flag should be true after first write")
	assert.Equal(t, http.StatusOK, rw.Status(), "status should be 200 after first write")

	n2, err2 := rw.Write(data2)
	require.NoError(t, err2, "second Write should not return error")
	assert.Equal(t, len(data2), n2, "second Write should return bytes written")
	assert.Equal(t, len(data1)+len(data2), rw.Size(), "size should accumulate after second write")

	n3, err3 := rw.Write(data3)
	require.NoError(t, err3, "third Write should not return error")
	assert.Equal(t, len(data3), n3, "third Write should return bytes written")
	assert.Equal(
		t,
		len(data1)+len(data2)+len(data3),
		rw.Size(),
		"size should accumulate after third write",
	)

	expectedTotal := len(data1) + len(data2) + len(data3)
	assert.Equal(t, expectedTotal, rw.Size(), "total size should match sum of all writes")
}

func TestResponseWriter_Status_EdgeCases(t *testing.T) {
	t.Run("status before any writes", func(t *testing.T) {
		originalWriter := httptest.NewRecorder()
		rw := newResponseWriter(originalWriter)

		assert.Equal(t, 0, rw.Status(), "status should be 0 before any writes")
		assert.False(t, rw.Written(), "written should be false before any writes")
	})

	t.Run("status after write without explicit header", func(t *testing.T) {
		originalWriter := httptest.NewRecorder()
		rw := newResponseWriter(originalWriter)

		_, err := rw.Write([]byte("test"))
		require.NoError(t, err, "Write should not return error")
		assert.Equal(
			t,
			http.StatusOK,
			rw.Status(),
			"status should be 200 when Write called without explicit header",
		)
		assert.True(t, rw.Written(), "written should be true after Write")
	})

	t.Run("status after explicit header then write", func(t *testing.T) {
		originalWriter := httptest.NewRecorder()
		rw := newResponseWriter(originalWriter)

		rw.WriteHeader(http.StatusNotFound)
		_, err := rw.Write([]byte("test"))
		require.NoError(t, err, "Write should not return error")
		assert.Equal(
			t,
			http.StatusNotFound,
			rw.Status(),
			"status should return explicit status, not 200",
		)
		assert.True(t, rw.Written(), "written should be true after Write")
	})

	t.Run("status when written but no explicit status", func(t *testing.T) {
		// This tests the specific condition in Status() method:
		// if rw.status == 0 && rw.written { return http.StatusOK }
		rw := &responseWriter{
			ResponseWriter: httptest.NewRecorder(),
			status:         0,    // No explicit status set
			written:        true, // But written flag is true
			size:           0,
		}

		// Should return 200 even though status is 0, because written is true
		assert.Equal(t, http.StatusOK, rw.Status())
	})
}

func TestResponseWriter_Size_AccumulatesCorrectly(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	tests := []struct {
		data     []byte
		expected int
	}{
		{[]byte("a"), 1},
		{[]byte("bb"), 3},    // cumulative: 1 + 2 = 3
		{[]byte("ccc"), 6},   // cumulative: 3 + 3 = 6
		{[]byte(""), 6},      // empty write doesn't change size
		{[]byte("dddd"), 10}, // cumulative: 6 + 4 = 10
	}

	for i, tt := range tests {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			n, err := rw.Write(tt.data)
			require.NoError(t, err, "Write should not return error")
			assert.Equal(t, len(tt.data), n, "Write should return number of bytes written")
			assert.Equal(t, tt.expected, rw.Size(), "Size should accumulate correctly")
		})
	}
}

func TestResponseWriter_InterfaceCompliance(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	// Verify that our ResponseWriter implements http.ResponseWriter
	var _ http.ResponseWriter = rw

	// Verify that our ResponseWriter implements our custom ResponseWriter interface
	_ = rw

	// Test that we can use it as http.ResponseWriter
	rw.Header().Set("Content-Type", "text/plain")
	rw.WriteHeader(http.StatusOK)

	data := []byte("test")
	n, err := rw.Write(data)
	require.NoError(t, err, "Write should not return error")
	assert.Equal(t, len(data), n, "Write should return number of bytes written")

	assert.Equal(
		t,
		"text/plain",
		originalWriter.Header().Get("Content-Type"),
		"headers should be passed through to underlying writer",
	)
}

func TestResponseWriter_HeaderOperations(t *testing.T) {
	originalWriter := httptest.NewRecorder()
	rw := newResponseWriter(originalWriter)

	// Test header operations
	rw.Header().Set("X-Custom-Header", "custom-value")
	rw.Header().Add("X-Multi-Header", "value1")
	rw.Header().Add("X-Multi-Header", "value2")

	assert.Equal(
		t,
		"custom-value",
		rw.Header().Get("X-Custom-Header"),
		"should get custom header from wrapper",
	)
	assert.Equal(
		t,
		"custom-value",
		originalWriter.Header().Get("X-Custom-Header"),
		"should get custom header from underlying writer",
	)

	multiValues := rw.Header().Values("X-Multi-Header")
	assert.Len(t, multiValues, 2, "should have 2 values for multi-header")
	assert.Contains(t, multiValues, "value1", "should contain first value")
	assert.Contains(t, multiValues, "value2", "should contain second value")
}

func TestResponseWriter_WriteError(t *testing.T) {
	// Create a failing writer to test error handling
	failingWriter := &failingResponseWriter{}
	rw := newResponseWriter(failingWriter)

	data := []byte("test data")
	n, err := rw.Write(data)

	require.Error(t, err, "should return error from underlying writer")
	assert.Equal(t, 0, n, "should return 0 bytes written on error")
	assert.Equal(t, 0, rw.Size(), "size should not be updated on error")
	assert.True(
		t,
		rw.Written(),
		"written flag should be set even on error (WriteHeader was called)",
	)
}

func TestResponseWriter_PartialWrite(t *testing.T) {
	// Create a writer that only writes partial data
	partialWriter := &partialResponseWriter{maxBytes: 5}
	rw := newResponseWriter(partialWriter)

	data := []byte("Hello, World!") // 13 bytes
	n, err := rw.Write(data)

	require.NoError(t, err, "partial write should not return error")
	assert.Equal(t, 5, n, "should write only maxBytes (5) bytes")
	assert.Equal(t, 5, rw.Size(), "size should match actual bytes written, not requested")
}

// Helper types for testing error conditions

type failingResponseWriter struct {
	header http.Header
}

func (f *failingResponseWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *failingResponseWriter) Write([]byte) (int, error) {
	return 0, assert.AnError // Always fail
}

func (f *failingResponseWriter) WriteHeader(int) {
	// Do nothing
}

type partialResponseWriter struct {
	header   http.Header
	maxBytes int
}

func (p *partialResponseWriter) Header() http.Header {
	if p.header == nil {
		p.header = make(http.Header)
	}
	return p.header
}

func (p *partialResponseWriter) Write(b []byte) (int, error) {
	if len(b) > p.maxBytes {
		return p.maxBytes, nil // Partial write
	}
	return len(b), nil
}

func (p *partialResponseWriter) WriteHeader(int) {
	// Do nothing
}
