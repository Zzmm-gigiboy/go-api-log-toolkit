package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Link struct {
	ID    int      `json:"id"`
	Title string   `json:"title"`
	URL   string   `json:"url"`
	Tags  []string `json:"tags"`
}
type Store struct {
	mu    sync.Mutex
	links []Link
	next  int
	path  string
}

func newStore(path string) *Store {
	s := &Store{path: path, next: 1}
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			if e := json.Unmarshal(b, &s.links); e == nil {
				for _, l := range s.links {
					if l.ID >= s.next {
						s.next = l.ID + 1
					}
				}
			}
		}
	}
	return s
}
func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, e := json.MarshalIndent(s.links, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(s.path, b, 0600)
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func (s *Store) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/links", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		res := []Link{}
		for _, l := range s.links {
			if q == "" || strings.Contains(strings.ToLower(l.Title+" "+strings.Join(l.Tags, " ")), q) {
				res = append(res, l)
			}
		}
		sort.Slice(res, func(i, j int) bool { return strings.ToLower(res[i].Title) < strings.ToLower(res[j].Title) })
		jsonResponse(w, 200, res)
	})
	mux.HandleFunc("POST /api/links", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var l Link
		if e := json.NewDecoder(r.Body).Decode(&l); e != nil {
			jsonResponse(w, 400, map[string]string{"error": "Invalid JSON"})
			return
		}
		parsed, e := url.ParseRequestURI(l.URL)
		if e != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(l.Title) == "" || len(l.Title) > 120 || len(l.Tags) > 10 {
			jsonResponse(w, 422, map[string]string{"error": "Invalid title, URL, or tags"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		l.ID = s.next
		s.next++
		l.Title = strings.TrimSpace(l.Title)
		s.links = append(s.links, l)
		if e := s.persist(); e != nil {
			s.links = s.links[:len(s.links)-1]
			s.next--
			jsonResponse(w, 500, map[string]string{"error": "Persistence failed"})
			return
		}
		jsonResponse(w, 201, l)
	})
	mux.HandleFunc("DELETE /api/links/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil {
			jsonResponse(w, 400, map[string]string{"error": "Invalid id"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		found := -1
		for i, l := range s.links {
			if l.ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			jsonResponse(w, 404, map[string]string{"error": "Not found"})
			return
		}
		original := append([]Link(nil), s.links...)
		s.links = append(s.links[:found], s.links[found+1:]...)
		if err := s.persist(); err != nil {
			s.links = original
			jsonResponse(w, 500, map[string]string{"error": "Persistence failed"})
			return
		}
		w.WriteHeader(204)
	})
	return mux
}
func run() error {
	path := os.Getenv("LINK_STORE_FILE")
	s := newStore(path)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("Link API on :%s\n", port)
	return http.ListenAndServe(":"+port, s.handler())
}
func main() {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
