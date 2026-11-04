# Technical Challenges & Architecture Rationale: `go-api-log-toolkit`

## Overview

The `go-api-log-toolkit` repository is a high-performance, zero-external-dependency Go micro-toolkit comprising two independent components:
1. **`go-link-api`**: A lightweight RESTful bookmarking microservice implementing in-memory caching, JSON disk persistence, and transactional rollback semantics.
2. **`go-log-insights`**: A memory-bounded streaming NDJSON (Newline Delimited JSON) telemetry analyzer computing real-time service metrics and failure rates.

Both utilities are engineered exclusively using the Go standard library (`net/http`, `sync`, `bufio`, `encoding/json`, `net/url`, `os`, `sort`). Below is an in-depth breakdown of the core architectural decisions, difficult technical hurdles, and engineering trade-offs faced during development and testing.

---

## Challenge 1: Native Go 1.22 Standard Routing (`r.PathValue`) vs External Routers

### Context & Historical Problem
For over a decade, Go backend microservices almost universally depended on third-party routing frameworks (such as `gorilla/mux`, `chi`, `gin`, or `echo`) because standard `net/http.ServeMux` lacked two critical routing primitives:
1. HTTP method filtering on route patterns (e.g. `GET /api/links` vs `POST /api/links`).
2. Wildcard path parameter capture (e.g. `/api/links/{id}`).

In Go 1.22, the standard library introduced enhanced pattern routing directly into `net/http.ServeMux`, enabling declarative method prefixes and named wildcards parsed via `r.PathValue(key)`.

### Technical Hurdle
Replacing third-party routers requires strict compliance with HTTP standards and idiomatic error handling:
- **Path Variable Extraction & Validation**: Unlike reflection-heavy or auto-binding frameworks, `r.PathValue("id")` yields a raw `string`. Handlers must parse this string defensively:
  ```go
  id, err := strconv.Atoi(r.PathValue("id"))
  if err != nil {
      jsonResponse(w, 400, map[string]string{"error": "Invalid id"})
      return
  }
  ```
  Distinguishing between **HTTP 400 (Bad Request)** for non-integer path parameters (e.g., `/api/links/foo` or `/api/links/12.34`) and **HTTP 404 (Not Found)** for non-existent IDs (e.g., `/api/links/9999`) is critical for RESTful correctness.
- **Defensive Request Body Capping**: In `POST /api/links`, malicious or misconfigured clients can stream gigabytes of garbage data into memory. The handler wraps the request body with `http.MaxBytesReader(w, r.Body, 1<<16)`, strictly bounding input to 64 KB before passing it to `json.NewDecoder`.

### Architectural Decision
By exclusively adopting Go 1.22's native `http.ServeMux`, the service eliminates all third-party dependencies, reduces supply-chain risk to zero, slashes build times, and produces tiny, self-contained binaries.

---

## Challenge 2: In-Memory Mutex Caching Paired with Durable Persistence & Transactional Rollback

### Context & Problem
`go-link-api` maintains an in-memory slice `[]Link` for sub-millisecond retrieval and filtering, while synchronously committing modifications to a local JSON file (`LINK_STORE_FILE`) for durability across process restarts.

In dual-state architectures (RAM cache backed by disk I/O), filesystem operations can fail unpredictably:
- Disk volume becomes full.
- Target directory has read-only permissions (`EACCES` / `EPERM`).
- Process killed or interrupted mid-write.

A naive implementation updates the in-memory slice first, then attempts `os.WriteFile`. If `os.WriteFile` errors out, the in-memory store remains mutated while the on-disk file remains unchanged. This causes **state drift**: client requests believe a record was created or deleted, but subsequent restarts reveal the operation never persisted.

### Implementation: Atomic In-Memory Rollback
To prevent state corruption, all mutating operations (`POST` and `DELETE`) execute transactional rollback protocols under a `sync.Mutex` lock:

#### 1. Creation Rollback (`POST /api/links`)
```go
s.mu.Lock()
defer s.mu.Unlock()

l.ID = s.next
s.next++
l.Title = strings.TrimSpace(l.Title)
s.links = append(s.links, l)

if err := s.persist(); err != nil {
    // Cleanly revert slice and auto-increment counter
    s.links = s.links[:len(s.links)-1]
    s.next--
    jsonResponse(w, 500, map[string]string{"error": "Persistence failed"})
    return
}
```

#### 2. Deletion Rollback (`DELETE /api/links/{id}`)
```go
s.mu.Lock()
defer s.mu.Unlock()

// Snapshot the slice before mutation
original := append([]Link(nil), s.links...)
s.links = append(s.links[:found], s.links[found+1:]...)

if err := s.persist(); err != nil {
    // Restore exact snapshot upon persistence failure
    s.links = original
    jsonResponse(w, 500, map[string]string{"error": "Persistence failed"})
    return
}
```

### Verification & Concurrency Safety
A dedicated unit test (`TestTransactionalRollback`) simulates filesystem write failures by pointing the store to an unwriteable directory path, asserting that:
1. The endpoint returns HTTP 500.
2. `len(s.links)` and `s.next` return to their pre-operation state without data corruption.
3. Multi-goroutine concurrency stress tests (`TestStoreConcurrency`) run 15 parallel workers executing simultaneous writes, reads, and deletes under Go's race detector to guarantee zero race conditions.

---

## Challenge 3: Memory-Bounded Streaming NDJSON Log Ingestion (`bufio.Scanner`)

### Context & Problem
Log analysis tools frequently ingest multi-gigabyte or streaming log files. Reading entire files into memory with `os.ReadFile` or `io.ReadAll` triggers catastrophic Out-Of-Memory (OOM) failures in containerized environments.

### Implementation: Line-by-Line Streaming
`go-log-insights` processes telemetry streams in constant $O(1)$ memory by utilizing `bufio.Scanner`:
```go
scanner := bufio.NewScanner(in)
for scanner.Scan() {
    line++
    data := scanner.Bytes()
    if len(data) == 0 {
        continue // Skip empty newlines with zero allocation
    }
    // Parse single record into stack-allocated struct
    var e Event
    if err := json.Unmarshal(data, &e); err != nil {
        return nil, fmt.Errorf("line %d: %w", line, err)
    }
    // Validate domain boundaries
    if e.Route == "" || e.DurationMs < 0 || e.Status < 100 || e.Status > 599 {
        return nil, fmt.Errorf("line %d: invalid event", line)
    }
    // Increment aggregate metrics in hash map
    ...
}
```

### Key Technical Hurdles & Resolutions
1. **Precise Line Attribution on Malformed Input**:
   When analyzing thousands of events, a generic error like `invalid JSON` is useless. By tracking an internal `line` counter that increments on every scanned line, parsing syntax errors and domain validation errors pinpoint the exact line number (`line %d: %w`).
2. **Buffer Limits & Oversized Lines**:
   `bufio.Scanner` enforces a default buffer cap of `bufio.MaxScanTokenSize = 64 * 1024` (64 KB). Lines exceeding this threshold cause `scanner.Scan()` to return `false`, and `scanner.Err()` returns `bufio.ErrTooLong`. A test (`TestMaxScanTokenSizeHandling`) validates that oversized log payloads are safely rejected rather than allocating unbounded heap memory.
3. **HTTP Status Code Boundary Categorization**:
   Per RFC 9110, HTTP status codes range from 100 to 599. Errors are strictly categorized:
   - 2xx (Success), 3xx (Redirect), and 4xx (Client Error) are tracked as requests but not counted as system reliability errors.
   - 5xx (Server Error: 500 Internal Error, 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout, 599 Network Timeout) increment the `Errors` metric.
   - Status codes `< 100` or `> 599` are rejected as invalid events.

---

## Challenge 4: Multi-Module Monorepo CI Orchestration & Race Detector Integration

### Context & Monorepo Structure
The repository is structured as a decoupled multi-module monorepo:
```
go-api-log-toolkit/
├── .github/workflows/ci.yml
├── go-link-api/
│   ├── go.mod        # module example.com/portfolio/go-link-api
│   ├── main.go
│   └── main_test.go
└── go-log-insights/
    ├── go.mod        # module example.com/portfolio/go-log-insights
    ├── main.go
    └── main_test.go
```
There is intentionally no root `go.mod` or `go.work` file to avoid artificial dependency coupling between the microservice and the CLI analyzer.

### Technical Hurdle
Running `go test ./...` from the repository root fails because Go requires a `go.mod` file in the current directory or parent tree for package resolution.

Furthermore, Go's race detector (`-race`) utilizes ThreadSanitizer runtime libraries, which require CGO (`CGO_ENABLED=1`) and a platform C compiler (`gcc` or `clang`). On Windows workstations without MinGW/GCC installed, running `go test -race` fails with:
```
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
```

### CI Pipeline Resolution
The GitHub Actions workflow (`.github/workflows/ci.yml`) is architected to address both challenges:
1. **Per-Module Traversal**: CI steps navigate into each module directory sequentially (`cd go-link-api`, `cd go-log-insights`).
2. **Standardized Linux Runner**: By targeting `ubuntu-latest`, GitHub Actions provides pre-installed GCC, enabling CGO and ThreadSanitizer out-of-the-box.
3. **Static Analysis & Testing**:
   - `go vet ./...`: Detects subtle code smells, shadowed variables, and unreachable code.
   - `go test -v -race ./...`: Runs the complete table-driven test suite with ThreadSanitizer active, validating mutex safety and catching concurrency anomalies.
4. **Trigger Scoping**: Triggers are locked to push and pull request events targeting the default branch `main`.

---

## Conclusion & Architectural Summary

By combining Go 1.22 standard library routing, in-memory transactional rollback, streaming NDJSON processing, and ThreadSanitizer CI verification, `go-api-log-toolkit` demonstrates how to build resilient, dependency-free micro-utilities with clean state management and memory safety.
