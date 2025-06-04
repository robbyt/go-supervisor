# Examples

Several working examples of go-supervisor usage.

## [http](./http/)
Basic HTTP server with graceful shutdown and configuration reloading.

## [custom_middleware](./custom_middleware/)
HTTP server with middleware that transforms responses.

## [composite](./composite/)
Multiple dynamic services managed as a single unit, using Generics.

## [httpcluster](./httpcluster/)
Similar to composite, but designed specifically for running several `httpserver` instances, with a channel-based config "siphon" for dynamic updates.

## Running

```bash
go run ./examples/<name>
```