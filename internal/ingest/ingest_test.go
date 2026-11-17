package ingest

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/trawlhq/trawl/internal/store"
)

func TestParseIngestion(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/sample.json")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	prices := map[string]store.Price{
		"gpt-4o-mini": {InputPer1k: 0.00015, OutputPer1k: 0.0006},
	}
	spans, scores, err := ParseIngestion(raw, "prj_test", prices)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spans) != 5 {
		t.Fatalf("want 5 spans, got %d", len(spans))
	}
	if len(scores) != 0 {
		t.Fatalf("want 0 scores, got %d", len(scores))
	}

	var gen *store.Span
	for i := range spans {
		if spans[i].SpanID == "gen_1" {
			gen = &spans[i]
		}
	}
	if gen == nil {
		t.Fatal("generation span missing")
	}
	if gen.Model != "gpt-4o-mini" {
		t.Errorf("model = %q", gen.Model)
	}
	if gen.CostDetails == "" {
		t.Error("cost not derived for priced model")
	}
	var cost map[string]float64
	if err := json.Unmarshal([]byte(gen.CostDetails), &cost); err != nil {
		t.Fatalf("cost json: %v", err)
	}
	if cost["total"] <= 0 {
		t.Errorf("cost total = %v", cost["total"])
	}

	for _, s := range spans {
		if s.SpanID == "span_retrieve" && s.EventTS == 0 {
			t.Error("event_ts missing")
		}
	}
}

func TestParseIngestionUnknownTypeIsSkipped(t *testing.T) {
	payload := []byte(`{"batch":[{"id":"x","type":"sdk-log","timestamp":"2026-11-16T14:00:00Z","body":{}}]}`)
	spans, scores, err := ParseIngestion(payload, "prj_test", nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spans) != 0 || len(scores) != 0 {
		t.Fatalf("expected empty output, got %d/%d", len(spans), len(scores))
	}
}

func TestParseOTLP(t *testing.T) {
	payload := []byte(`{"resourceSpans":[{"scopeSpans":[{"scope":{"name":"test"},
	  "spans":[{"traceId":"t1","spanId":"s1","name":"chat","startTimeUnixNano":"1794837600000000000",
	  "endTimeUnixNano":"1794837601500000000","attributes":[
	    {"key":"gen_ai.request.model","value":{"stringValue":"gpt-4o-mini"}},
	    {"key":"gen_ai.usage.input_tokens","value":{"stringValue":"100"}}],
	  "status":{"code":2,"message":"boom"}}]}]}]}`)
	spans, err := ParseOTLP(payload, "prj_test", map[string]store.Price{"gpt-4o-mini": {InputPer1k: 0.002, OutputPer1k: 0.004}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Type != "generation" {
		t.Errorf("type = %q", s.Type)
	}
	if s.Level != "ERROR" {
		t.Errorf("level = %q", s.Level)
	}
	if s.EndTime == nil || *s.EndTime-s.StartTime != 1500 {
		t.Errorf("duration wrong: %v", s.EndTime)
	}
}

func TestParseUsageFieldIsHonored(t *testing.T) {
	payload := []byte(`{"batch":[{"id":"e1","type":"generation-create","timestamp":"2026-11-17T00:00:00Z",
		"body":{"id":"g1","traceId":"t1","name":"answer","startTime":"2026-11-17T00:00:00Z",
		        "model":"gpt-4o-mini","usage":{"input":100,"output":200}}}]}`)
	prices := map[string]store.Price{"gpt-4o-mini": {InputPer1k: 0.00015, OutputPer1k: 0.0006}}
	spans, _, err := ParseIngestion(payload, "p1", prices)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	var usage map[string]int64
	if err := json.Unmarshal([]byte(spans[0].UsageDetails), &usage); err != nil {
		t.Fatalf("usage json: %v (%q)", err, spans[0].UsageDetails)
	}
	if usage["input"] != 100 || usage["output"] != 200 || usage["total"] != 300 {
		t.Fatalf("usage = %v, want input=100 output=200 total=300", usage)
	}
	if spans[0].CostDetails == "" {
		t.Fatal("cost should be derived from usage + seeded price")
	}
}
