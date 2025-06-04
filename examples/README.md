# Examples

Working code demonstrating go-supervisor usage.

## [http](./http/)
Basic HTTP server with graceful shutdown and configuration reloading.

## [custom_middleware](./custom_middleware/)
HTTP server with middleware that transforms responses.

## [composite](./composite/)
Multiple services managed as a single unit.

## [httpcluster](./httpcluster/)
HTTP server cluster with automatic port allocation.

## Running

```bash
go run ./examples/<name>
```