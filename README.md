# Go API and Log Toolkit

Two small Go programs demonstrating HTTP APIs, JSON persistence, validation, and log aggregation.

## REST API

```bash
cd go-link-api
go test ./...
go run .
```

The API starts on port 8080. `GET http://localhost:8080/health` responds with service status. `LINK_STORE_FILE` configures the local JSON storage file.

## Log CLI

```bash
cd go-log-insights
go test ./...
go run . sample.ndjson
```

The included NDJSON file contains artificial example log records. No real user logs or credentials are included. Do not expose the unprotected API on the internet.
