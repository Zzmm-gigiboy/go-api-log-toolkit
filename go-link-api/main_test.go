package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
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
