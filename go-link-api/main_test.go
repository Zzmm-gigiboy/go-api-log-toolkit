package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLinksCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.json")
	h := newStore(path).handler()
	request := func(method, p string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, p, bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if got := request("GET", "/health", nil).Code; got != 200 {
		t.Fatalf("health: %d", got)
	}
	if got := request("POST", "/api/links", []byte(`{"title":"Bad","url":"javascript:alert(1)"}`)).Code; got != 422 {
		t.Fatalf("should reject URL: %d", got)
	}
	created := request("POST", "/api/links", []byte(`{"title":"Go Docs","url":"https://go.dev/doc/","tags":["language"]}`))
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var l Link
	if err := json.Unmarshal(created.Body.Bytes(), &l); err != nil || l.ID != 1 {
		t.Fatalf("invalid response: %v", err)
	}
	listed := request("GET", "/api/links?q=language", nil)
	var items []Link
	if err := json.Unmarshal(listed.Body.Bytes(), &items); err != nil || len(items) != 1 {
		t.Fatalf("search failed: %s", listed.Body)
	}
	if len(newStore(path).links) != 1 {
		t.Fatal("JSON storage did not persist")
	}
	if got := request("DELETE", "/api/links/1", nil).Code; got != 204 {
		t.Fatalf("delete: %d", got)
	}
	if got := request("DELETE", "/api/links/1", nil).Code; got != 404 {
		t.Fatalf("404 expected: %d", got)
	}
}

func TestPostValidationTableDriven(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		expectedCode int
	}{
		// Malformed JSON payloads -> 400 Bad Request
		{
			name:         "Malformed JSON: truncated object",
			payload:      `{"title": "Incomplete`,
			expectedCode: 400,
		},
		{
			name:         "Malformed JSON: unquoted key",
			payload:      `{title: "Bad syntax", url: "https://example.com"}`,
			expectedCode: 400,
		},
		{
			name:         "Malformed JSON: empty string body",
			payload:      ``,
			expectedCode: 400,
		},
		{
			name:         "Malformed JSON: raw non-json text",
			payload:      `not a json payload at all`,
			expectedCode: 400,
		},

		// URL Validation Edge Cases -> 422 Unprocessable Entity
		{
			name:         "URL missing scheme",
			payload:      `{"title":"No Scheme","url":"example.com"}`,
			expectedCode: 422,
		},
		{
			name:         "URL scheme-relative without scheme",
			payload:      `{"title":"Relative Scheme","url":"//example.com/path"}`,
			expectedCode: 422,
		},
		{
			name:         "URL non-http ftp scheme",
			payload:      `{"title":"FTP Link","url":"ftp://files.example.com"}`,
			expectedCode: 422,
		},
		{
			name:         "URL non-http file scheme",
			payload:      `{"title":"File Link","url":"file:///etc/passwd"}`,
			expectedCode: 422,
		},
		{
			name:         "URL xss javascript scheme",
			payload:      `{"title":"XSS Link","url":"javascript:alert(1)"}`,
			expectedCode: 422,
		},
		{
			name:         "URL data scheme",
			payload:      `{"title":"Data Link","url":"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg=="}`,
			expectedCode: 422,
		},
		{
			name:         "URL missing host (http://)",
			payload:      `{"title":"Missing Host","url":"http://"}`,
			expectedCode: 422,
		},
		{
			name:         "URL missing host (https://)",
			payload:      `{"title":"Missing Host HTTPS","url":"https://"}`,
			expectedCode: 422,
		},
		{
			name:         "URL malformed URI syntax",
			payload:      `{"title":"Malformed URI","url":"http://[invalid-ipv6/test"}`,
			expectedCode: 422,
		},

		// Title Length & Content Boundaries
		{
			name:         "Title empty string",
			payload:      `{"title":"","url":"https://example.com"}`,
			expectedCode: 422,
		},
		{
			name:         "Title whitespace only",
			payload:      `{"title":"    \t \n  ","url":"https://example.com"}`,
			expectedCode: 422,
		},
		{
			name:         "Title exactly 120 characters boundary (valid)",
			payload:      fmt.Sprintf(`{"title":"%s","url":"https://example.com"}`, strings.Repeat("a", 120)),
			expectedCode: 201,
		},
		{
			name:         "Title 121 characters (exceeds max limit)",
			payload:      fmt.Sprintf(`{"title":"%s","url":"https://example.com"}`, strings.Repeat("a", 121)),
			expectedCode: 422,
		},
		{
			name:         "Title unicode characters",
			payload:      `{"title":"Go 语言 🚀 Программирование & Testing","url":"https://golang.org"}`,
			expectedCode: 201,
		},

		// Tag Boundaries
		{
			name:         "Tags nil/omitted",
			payload:      `{"title":"Omitted Tags","url":"https://example.com"}`,
			expectedCode: 201,
		},
		{
			name:         "Tags empty array",
			payload:      `{"title":"Empty Tags","url":"https://example.com","tags":[]}`,
			expectedCode: 201,
		},
		{
			name:         "Tags exactly 10 tags (boundary valid)",
			payload:      `{"title":"Ten Tags","url":"https://example.com","tags":["1","2","3","4","5","6","7","8","9","10"]}`,
			expectedCode: 201,
		},
		{
			name:         "Tags 11 tags (exceeds limit)",
			payload:      `{"title":"Eleven Tags","url":"https://example.com","tags":["1","2","3","4","5","6","7","8","9","10","11"]}`,
			expectedCode: 422,
		},

		// Valid URL Variants
		{
			name:         "Valid HTTP localhost with port",
			payload:      `{"title":"Localhost Dev","url":"http://localhost:8080/debug"}`,
			expectedCode: 201,
		},
		{
			name:         "Valid IPv4 address with port",
			payload:      `{"title":"IPv4 Endpoint","url":"http://127.0.0.1:3000/api/v1"}`,
			expectedCode: 201,
		},
		{
			name:         "Valid International Domain Name (punycode)",
			payload:      `{"title":"Punycode IDN","url":"https://xn--mnchen-3ya.de/portal"}`,
			expectedCode: 201,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test_validation.json")
			h := newStore(path).handler()

			r := httptest.NewRequest("POST", "/api/links", strings.NewReader(tc.payload))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tc.expectedCode {
				t.Fatalf("expected status %d for %q, got %d. Body: %s",
					tc.expectedCode, tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestTitleWhitespaceTrimming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trim_test.json")
	h := newStore(path).handler()

	payload := `{"title":"   Surrounded By Spaces   ","url":"https://example.com"}`
	r := httptest.NewRequest("POST", "/api/links", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	var created Link
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if created.Title != "Surrounded By Spaces" {
		t.Fatalf("expected title to be trimmed to 'Surrounded By Spaces', got %q", created.Title)
	}
}

func TestSearchFilteringAndSorting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search_test.json")
	store := newStore(path)
	h := store.handler()

	linksToCreate := []string{
		`{"title":"Zeta Service","url":"https://zeta.internal/","tags":["backend","grpc"]}`,
		`{"title":"Alpha Core Engine","url":"https://alpha.internal/","tags":["core","algorithms"]}`,
		`{"title":"Beta Gateway","url":"https://beta.internal/","tags":["gateway","backend"]}`,
		`{"title":"Gamma Console","url":"https://gamma.internal/","tags":["frontend","react"]}`,
	}

	for _, payload := range linksToCreate {
		r := httptest.NewRequest("POST", "/api/links", strings.NewReader(payload))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("failed to insert test link: %d", w.Code)
		}
	}

	t.Run("Empty query returns all links sorted alphabetically", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/links", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var results []Link
		if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if len(results) != 4 {
			t.Fatalf("expected 4 links, got %d", len(results))
		}
		expectedOrder := []string{"Alpha Core Engine", "Beta Gateway", "Gamma Console", "Zeta Service"}
		for i, exp := range expectedOrder {
			if results[i].Title != exp {
				t.Fatalf("at index %d: expected title %q, got %q", i, exp, results[i].Title)
			}
		}
	})

	t.Run("Query matches tag with case insensitivity", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/links?q=BACKEND", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var results []Link
		if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 matches for 'BACKEND', got %d", len(results))
		}
		if results[0].Title != "Beta Gateway" || results[1].Title != "Zeta Service" {
			t.Fatalf("unexpected search results: %#v", results)
		}
	})

	t.Run("Query matches title substring", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/links?q=engine", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		var results []Link
		if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if len(results) != 1 || results[0].Title != "Alpha Core Engine" {
			t.Fatalf("expected Alpha Core Engine, got %#v", results)
		}
	})

	t.Run("Query with no matches returns empty array", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/links?q=nonexistent_token_xyz", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var results []Link
		if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(results))
		}
		if !strings.Contains(w.Body.String(), "[]") {
			t.Fatalf("expected JSON array '[]', got %q", w.Body.String())
		}
	})
}

func TestDeleteRouteEdgeCases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delete_test.json")
	store := newStore(path)
	h := store.handler()

	// Seed 1 link with ID = 1
	rInit := httptest.NewRequest("POST", "/api/links", strings.NewReader(`{"title":"Initial","url":"https://example.com"}`))
	wInit := httptest.NewRecorder()
	h.ServeHTTP(wInit, rInit)
	if wInit.Code != 201 {
		t.Fatalf("failed to seed initial link: %d", wInit.Code)
	}

	tests := []struct {
		name         string
		path         string
		expectedCode int
	}{
		{
			name:         "Non-integer alphabetical ID returns 400",
			path:         "/api/links/not-an-id",
			expectedCode: 400,
		},
		{
			name:         "Float numeric ID returns 400",
			path:         "/api/links/12.34",
			expectedCode: 400,
		},
		{
			name:         "Negative ID returns 404",
			path:         "/api/links/-1",
			expectedCode: 404,
		},
		{
			name:         "Non-existent positive ID returns 404",
			path:         "/api/links/99999",
			expectedCode: 404,
		},
		{
			name:         "Valid existing ID returns 204",
			path:         "/api/links/1",
			expectedCode: 204,
		},
		{
			name:         "Repeated deletion of same ID returns 404",
			path:         "/api/links/1",
			expectedCode: 404,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("DELETE", tc.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.expectedCode {
				t.Fatalf("expected %d for %q, got %d. Body: %s", tc.expectedCode, tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestTransactionalRollback(t *testing.T) {
	t.Run("POST rolls back in-memory state on persistence failure", func(t *testing.T) {
		tempDir := t.TempDir()
		// Setting path to an existing directory causes os.WriteFile to fail
		unwritablePath := filepath.Join(tempDir, "read_only_dir")
		if err := os.Mkdir(unwritablePath, 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}

		store := newStore(unwritablePath)
		h := store.handler()

		initialLinksCount := len(store.links)
		initialNext := store.next

		r := httptest.NewRequest("POST", "/api/links", strings.NewReader(`{"title":"Failing Write","url":"https://example.com"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != 500 {
			t.Fatalf("expected status 500 on persist failure, got %d. Body: %s", w.Code, w.Body.String())
		}

		// Verify state was reverted cleanly
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.links) != initialLinksCount {
			t.Fatalf("store.links was not rolled back: expected %d, got %d", initialLinksCount, len(store.links))
		}
		if store.next != initialNext {
			t.Fatalf("store.next was not rolled back: expected %d, got %d", initialNext, store.next)
		}
	})

	t.Run("DELETE rolls back in-memory state on persistence failure", func(t *testing.T) {
		tempDir := t.TempDir()
		unwritablePath := filepath.Join(tempDir, "unwritable_dir")
		if err := os.Mkdir(unwritablePath, 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}

		store := newStore(unwritablePath)
		// Manually populate in-memory links before write failure
		store.links = []Link{
			{ID: 1, Title: "Existing 1", URL: "https://example.com/1"},
			{ID: 2, Title: "Existing 2", URL: "https://example.com/2"},
		}
		store.next = 3
		h := store.handler()

		r := httptest.NewRequest("DELETE", "/api/links/1", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != 500 {
			t.Fatalf("expected status 500 on persist failure, got %d. Body: %s", w.Code, w.Body.String())
		}

		// Verify deletion was rolled back
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.links) != 2 {
			t.Fatalf("store.links was not rolled back after failed delete: expected 2, got %d", len(store.links))
		}
		if store.links[0].ID != 1 || store.links[1].ID != 2 {
			t.Fatalf("store.links elements corrupted during rollback: %#v", store.links)
		}
	})
}

func TestStoreInitialization(t *testing.T) {
	t.Run("Initializes empty store when path is empty", func(t *testing.T) {
		s := newStore("")
		if s.next != 1 || len(s.links) != 0 {
			t.Fatalf("unexpected initial store state: next=%d, len=%d", s.next, len(s.links))
		}
		if err := s.persist(); err != nil {
			t.Fatalf("persist on empty path should succeed, got %v", err)
		}
	})

	t.Run("Recovers next ID from existing file correctly", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "existing_links.json")
		existingData := []Link{
			{ID: 4, Title: "Link 4", URL: "https://four.com"},
			{ID: 15, Title: "Link 15", URL: "https://fifteen.com"},
			{ID: 7, Title: "Link 7", URL: "https://seven.com"},
		}
		b, err := json.Marshal(existingData)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatalf("write error: %v", err)
		}

		s := newStore(path)
		if len(s.links) != 3 {
			t.Fatalf("expected 3 links loaded, got %d", len(s.links))
		}
		if s.next != 16 {
			t.Fatalf("expected next ID to be 16 (max+1), got %d", s.next)
		}
	})

	t.Run("Recovers gracefully from corrupted file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.json")
		if err := os.WriteFile(path, []byte(`{not valid json`), 0600); err != nil {
			t.Fatalf("write error: %v", err)
		}

		s := newStore(path)
		if s.next != 1 || len(s.links) != 0 {
			t.Fatalf("expected fresh store on corrupt file, got next=%d, len=%d", s.next, len(s.links))
		}
	})
}

func TestStoreConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrency_test.json")
	store := newStore(path)
	h := store.handler()

	const numWorkers = 15
	const opsPerWorker = 30
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for workerID := 0; workerID < numWorkers; workerID++ {
		go func(id int) {
			defer wg.Done()
			for op := 0; op < opsPerWorker; op++ {
				switch op % 3 {
				case 0: // Create
					payload := fmt.Sprintf(`{"title":"Worker %d Op %d","url":"https://worker%d.com/%d","tags":["concurrency","worker"]}`, id, op, id, op)
					r := httptest.NewRequest("POST", "/api/links", strings.NewReader(payload))
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != 201 {
						t.Errorf("worker %d create failed: %d", id, w.Code)
					}
				case 1: // Read / Search
					r := httptest.NewRequest("GET", "/api/links?q=concurrency", nil)
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != 200 {
						t.Errorf("worker %d search failed: %d", id, w.Code)
					}
				case 2: // Delete (attempt ID from current range)
					targetID := (op % 10) + 1
					r := httptest.NewRequest("DELETE", fmt.Sprintf("/api/links/%d", targetID), nil)
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					// Either 204 or 404 is valid depending on whether target exists
					if w.Code != 204 && w.Code != 404 {
						t.Errorf("worker %d delete unexpected code: %d", id, w.Code)
					}
				}
			}
		}(workerID)
	}

	wg.Wait()

	// Verify the final store state is valid
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.next <= 1 {
		t.Fatalf("expected store.next to have incremented, got %d", store.next)
	}
}
