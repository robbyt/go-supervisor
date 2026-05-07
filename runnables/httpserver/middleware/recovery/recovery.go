package recovery

import (
	"log/slog"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that recovers from panics in HTTP handlers,
// writes a 500 Internal Server Error response, and aborts the chain.
// When handler is non-nil, the recovered panic is logged at Error level.
//
// Note: if the handler panics AFTER it has already called WriteHeader or
// Write, the response status is already committed and cannot be replaced
// with 500. In that case the middleware preserves the partial response
// and flushes any pending data so the client is not left waiting; the
// panic still appears in the log with headers_written=true so operators
// can identify the request.
//
// If handler is nil, recovery is silent.
func New(handler slog.Handler) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		defer func() {
			if err := recover(); err != nil {
				req := rp.Request()
				writer := rp.Writer()
				headersWritten := writer.Written()

				if handler != nil {
					slog.New(handler).Error("HTTP handler panic recovered",
						"error", err,
						"path", req.URL.Path,
						"method", req.Method,
						"headers_written", headersWritten,
					)
				}

				if headersWritten {
					// Status is committed; preserve the partial response and
					// flush any buffered body so the client doesn't hang.
					if f, ok := writer.(http.Flusher); ok {
						f.Flush()
					}
				} else {
					http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				}
				rp.Abort()
			}
		}()

		rp.Next()
	}
}
