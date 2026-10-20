package main

import (
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
