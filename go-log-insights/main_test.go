package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestAnalyzer(t *testing.T) {
	input := `{"route":"/products","status":200,"duration_ms":40}
{"route":"/products","status":503,"duration_ms":60}
{"route":"/health","status":200,"duration_ms":2}`
	results, err := analyze(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[1].Route != "/products" || results[1].Requests != 2 || results[1].Errors != 1 || results[1].AvgMs != 50 {
		t.Fatalf("bad aggregation: %#v", results)
	}
	if _, err := analyze(strings.NewReader(`{"route":"/broken","status":999,"duration_ms":2}`)); err == nil {
		t.Fatal("bad event should fail")
	}
}

func TestEmptyAndBlankLines(t *testing.T) {
	t.Run("Empty stream returns empty slice", func(t *testing.T) {
		results, err := analyze(strings.NewReader(""))
		if err != nil {
			t.Fatalf("unexpected error on empty input: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 metrics, got %d", len(results))
		}
	})

	t.Run("Stream with consecutive empty lines skips blanks cleanly", func(t *testing.T) {
		input := `

{"route":"/ping","status":200,"duration_ms":10}


{"route":"/ping","status":200,"duration_ms":30}

`
		results, err := analyze(strings.NewReader(input))
		if err != nil {
			t.Fatalf("unexpected error with blank lines: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 route, got %d", len(results))
		}
		if results[0].Route != "/ping" || results[0].Requests != 2 || results[0].AvgMs != 20.0 {
			t.Fatalf("unexpected metric aggregation: %#v", results[0])
		}
	})
}

func TestStatusCodeCategorizationTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		shouldCountAsError bool
	}{
		// Standard 2xx Success (not an error)
		{name: "Status 200 OK", status: 200, shouldCountAsError: false},
		{name: "Status 201 Created", status: 201, shouldCountAsError: false},
		{name: "Status 204 No Content", status: 204, shouldCountAsError: false},

		// Standard 3xx Redirection (not an error)
		{name: "Status 301 Moved Permanently", status: 301, shouldCountAsError: false},
		{name: "Status 304 Not Modified", status: 304, shouldCountAsError: false},

		// Standard 4xx Client Errors (per analyzer logic: not a 5xx server error)
		{name: "Status 400 Bad Request", status: 400, shouldCountAsError: false},
		{name: "Status 401 Unauthorized", status: 401, shouldCountAsError: false},
		{name: "Status 403 Forbidden", status: 403, shouldCountAsError: false},
		{name: "Status 404 Not Found", status: 404, shouldCountAsError: false},
		{name: "Status 422 Unprocessable Entity", status: 422, shouldCountAsError: false},
		{name: "Status 429 Too Many Requests", status: 429, shouldCountAsError: false},
		{name: "Status 499 Client Closed Request boundary", status: 499, shouldCountAsError: false},

		// Standard 5xx Server Errors (counted as error in analyzer)
		{name: "Status 500 Internal Server Error boundary", status: 500, shouldCountAsError: true},
		{name: "Status 502 Bad Gateway", status: 502, shouldCountAsError: true},
		{name: "Status 503 Service Unavailable", status: 503, shouldCountAsError: true},
		{name: "Status 504 Gateway Timeout", status: 504, shouldCountAsError: true},
		{name: "Status 599 Network Connect Timeout boundary", status: 599, shouldCountAsError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"route":"/test","status":%d,"duration_ms":100}`, tc.status)
			res, err := analyze(strings.NewReader(input))
			if err != nil {
				t.Fatalf("unexpected error for status %d: %v", tc.status, err)
			}
			if len(res) != 1 {
				t.Fatalf("expected 1 result, got %d", len(res))
			}
			expectedErrors := 0
			if tc.shouldCountAsError {
				expectedErrors = 1
			}
			if res[0].Errors != expectedErrors {
				t.Fatalf("for status %d: expected Errors=%d, got %d", tc.status, expectedErrors, res[0].Errors)
			}
		})
	}
}

func TestEventValidationTableDriven(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedErrSub string
	}{
		{
			name:          "Empty route string",
			input:         `{"route":"","status":200,"duration_ms":15}`,
			expectedErrSub: "line 1: invalid event",
		},
		{
			name:          "Status below valid HTTP range (99)",
			input:         `{"route":"/api","status":99,"duration_ms":15}`,
			expectedErrSub: "line 1: invalid event",
		},
		{
			name:          "Status zero",
			input:         `{"route":"/api","status":0,"duration_ms":15}`,
			expectedErrSub: "line 1: invalid event",
		},
		{
			name:          "Status above valid HTTP range (600)",
			input:         `{"route":"/api","status":600,"duration_ms":15}`,
			expectedErrSub: "line 1: invalid event",
		},
		{
			name:          "Negative duration_ms",
			input:         `{"route":"/api","status":200,"duration_ms":-0.1}`,
			expectedErrSub: "line 1: invalid event",
		},
		{
			name:          "Corrupt JSON syntax",
			input:         `{"route":"/api","status":200,"duration_ms":`,
			expectedErrSub: "line 1: unexpected end of JSON",
		},
		{
			name:          "Comment/plain text line",
			input:         `# This is a comment header`,
			expectedErrSub: "line 1: invalid character",
		},
		{
			name:          "Invalid event on line 3 after valid and blank lines",
			input:         "{\"route\":\"/valid\",\"status\":200,\"duration_ms\":10}\n\n{\"route\":\"/bad\",\"status\":900,\"duration_ms\":10}",
			expectedErrSub: "line 3: invalid event",
		},
		{
			name:          "Wrong type for status (string instead of int)",
			input:         `{"route":"/api","status":"200","duration_ms":10}`,
			expectedErrSub: "line 1: json: cannot unmarshal string into Go struct field Event.status of type int",
		},
		{
			name:          "Wrong type for duration_ms (string instead of float)",
			input:         `{"route":"/api","status":200,"duration_ms":"fast"}`,
			expectedErrSub: "line 1: json: cannot unmarshal string into Go struct field Event.duration_ms of type float64",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := analyze(strings.NewReader(tc.input))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.expectedErrSub)
			}
			if !strings.Contains(err.Error(), tc.expectedErrSub) {
				t.Fatalf("expected error substring %q, got %q", tc.expectedErrSub, err.Error())
			}
		})
	}
}

func TestZeroDurationAndBoundaryStatuses(t *testing.T) {
	// Boundary valid status codes: 100 (Continue) and 599
	// Duration: exactly 0.0 (e.g. sub-millisecond cached responses)
	input := `{"route":"/cached","status":100,"duration_ms":0}
{"route":"/cached","status":599,"duration_ms":0}`
	results, err := analyze(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Route != "/cached" || results[0].Requests != 2 || results[0].Errors != 1 || results[0].AvgMs != 0.0 {
		t.Fatalf("unexpected boundary results: %#v", results[0])
	}
}

func TestMultiRouteAggregationAndSorting(t *testing.T) {
	input := `{"route":"/users","status":200,"duration_ms":50}
{"route":"/checkout","status":200,"duration_ms":120}
{"route":"/users","status":500,"duration_ms":70}
{"route":"/auth/login","status":200,"duration_ms":30}
{"route":"/checkout","status":503,"duration_ms":180}
{"route":"/auth/login","status":401,"duration_ms":20}
{"route":"/assets/app.js","status":200,"duration_ms":5}`

	results, err := analyze(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 distinct routes, got %d", len(results))
	}

	// Alphabetical order: /assets/app.js, /auth/login, /checkout, /users
	expectedRoutes := []string{"/assets/app.js", "/auth/login", "/checkout", "/users"}
	for i, exp := range expectedRoutes {
		if results[i].Route != exp {
			t.Fatalf("at index %d: expected route %q, got %q", i, exp, results[i].Route)
		}
	}

	// Verify /auth/login: 2 requests, 0 errors, avg (30+20)/2 = 25.0
	login := results[1]
	if login.Requests != 2 || login.Errors != 0 || math.Abs(login.AvgMs-25.0) > 1e-6 {
		t.Fatalf("unexpected metrics for /auth/login: %#v", login)
	}

	// Verify /checkout: 2 requests, 1 error (503), avg (120+180)/2 = 150.0
	checkout := results[2]
	if checkout.Requests != 2 || checkout.Errors != 1 || math.Abs(checkout.AvgMs-150.0) > 1e-6 {
		t.Fatalf("unexpected metrics for /checkout: %#v", checkout)
	}

	// Verify /users: 2 requests, 1 error (500), avg (50+70)/2 = 60.0
	users := results[3]
	if users.Requests != 2 || users.Errors != 1 || math.Abs(users.AvgMs-60.0) > 1e-6 {
		t.Fatalf("unexpected metrics for /users: %#v", users)
	}
}

func TestLargeLogStreams(t *testing.T) {
	const totalLines = 5000
	var sb strings.Builder
	for i := 0; i < totalLines; i++ {
		status := 200
		if i%10 == 0 {
			status = 500
		}
		sb.WriteString(fmt.Sprintf(`{"route":"/high-volume","status":%d,"duration_ms":10.0}`+"\n", status))
	}

	results, err := analyze(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatalf("unexpected error processing %d lines: %v", totalLines, err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 route, got %d", len(results))
	}
	metric := results[0]
	if metric.Requests != totalLines {
		t.Fatalf("expected %d requests, got %d", totalLines, metric.Requests)
	}
	if metric.Errors != 500 {
		t.Fatalf("expected 500 errors, got %d", metric.Errors)
	}
	if math.Abs(metric.AvgMs-10.0) > 1e-6 {
		t.Fatalf("expected avg 10.0, got %f", metric.AvgMs)
	}
}

func TestMaxScanTokenSizeHandling(t *testing.T) {
	t.Run("Line exceeding bufio.MaxScanTokenSize returns ErrTooLong", func(t *testing.T) {
		// bufio.MaxScanTokenSize is 64 * 1024 = 65536 bytes
		hugeRoute := "/" + strings.Repeat("a", bufio.MaxScanTokenSize+100)
		hugeLine := fmt.Sprintf(`{"route":"%s","status":200,"duration_ms":10}`, hugeRoute)

		_, err := analyze(strings.NewReader(hugeLine))
		if err == nil {
			t.Fatal("expected error on oversized line, got nil")
		}
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Fatalf("expected bufio.ErrTooLong, got %v", err)
		}
	})

	t.Run("Substantial line below MaxScanTokenSize succeeds", func(t *testing.T) {
		largeRoute := "/" + strings.Repeat("b", 10000)
		line := fmt.Sprintf(`{"route":"%s","status":200,"duration_ms":25}`, largeRoute)

		results, err := analyze(strings.NewReader(line))
		if err != nil {
			t.Fatalf("unexpected error for 10KB line: %v", err)
		}
		if len(results) != 1 || results[0].Route != largeRoute {
			t.Fatalf("failed to aggregate large valid line")
		}
	})
}
