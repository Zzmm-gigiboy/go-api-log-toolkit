package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type Event struct {
	Route      string  `json:"route"`
	Status     int     `json:"status"`
	DurationMs float64 `json:"duration_ms"`
}
type Metrics struct {
	Route    string  `json:"route"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	AvgMs    float64 `json:"avg_ms"`
}

func analyze(in io.Reader) ([]Metrics, error) {
	scanner := bufio.NewScanner(in)
	type agg struct {
		count, errors int
		sum           float64
	}
	groups := map[string]*agg{}
	line := 0
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if len(data) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if e.Route == "" || e.DurationMs < 0 || e.Status < 100 || e.Status > 599 {
			return nil, fmt.Errorf("line %d: invalid event", line)
		}
		a, ok := groups[e.Route]
		if !ok {
			a = &agg{}
			groups[e.Route] = a
		}
		a.count++
		a.sum += e.DurationMs
		if e.Status >= 500 {
			a.errors++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	results := []Metrics{}
	for route, a := range groups {
		results = append(results, Metrics{Route: route, Requests: a.count, Errors: a.errors, AvgMs: a.sum / float64(a.count)})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Route < results[j].Route })
	return results, nil
}
func main() {
	in := io.Reader(os.Stdin)
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	metrics, err := analyze(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(metrics); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
