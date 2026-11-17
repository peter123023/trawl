package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/trawlhq/trawl/internal/store"
	"github.com/trawlhq/trawl/internal/writer"
)

func TestIngestionFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	projectID, _, secret, err := st.Bootstrap(ctx)
	if err != nil || secret == "" {
		t.Fatalf("bootstrap: %v", err)
	}

	wr := writer.New(st, 64)
	wr.Start(ctx)
	api := New(st, wr, Options{ProjectID: projectID, Prices: map[string]store.Price{"gpt-4o-mini": {InputPer1k: 0.00015, OutputPer1k: 0.0006}}})
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	payload, err := os.ReadFile("../../testdata/sample.json")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/public/ingestion", bytes.NewReader(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/public/ingestion", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	var ing struct {
		Successes []map[string]any `json:"successes"`
		Errors    []map[string]any `json:"errors"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ing)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(ing.Successes) == 0 {
		t.Fatalf("ingest failed: %d %+v", resp.StatusCode, ing)
	}

	var traces struct {
		Traces []store.TraceSummary `json:"traces"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, err := http.Get(srv.URL + "/api/traces?limit=10")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		_ = json.NewDecoder(r.Body).Decode(&traces)
		r.Body.Close()
		if len(traces.Traces) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(60 * time.Millisecond)
	}
	if len(traces.Traces) != 1 {
		t.Fatalf("traces not flushed: %+v", traces)
	}
	if traces.Traces[0].SpanCount != 3 {
		t.Errorf("spanCount = %d, want 3 (pseudo trace row excluded)", traces.Traces[0].SpanCount)
	}
	if traces.Traces[0].Name != "refund_flow_v2" {
		t.Errorf("trace name = %q", traces.Traces[0].Name)
	}

	r, err := http.Get(srv.URL + "/api/traces/trace_001")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	var detail struct {
		Spans  []store.Span  `json:"spans"`
		Scores []store.Score `json:"scores"`
	}
	_ = json.NewDecoder(r.Body).Decode(&detail)
	r.Body.Close()
	if len(detail.Spans) != 4 {
		t.Fatalf("want 4 span rows, got %d", len(detail.Spans))
	}
	var merged bool
	for _, s := range detail.Spans {
		if s.SpanID == "span_retrieve" && s.Output == "命中 policy#12（退款时效）；置信度 0.91" {
			merged = true
		}
	}
	if !merged {
		t.Error("span update did not merge")
	}

	body := []byte(`{"traceId":"trace_001","spanId":"gen_1","name":"faithful","value":2,"comment":"ok"}`)
	r, err = http.Post(srv.URL+"/api/scores", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	var scored struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&scored)
	r.Body.Close()
	if scored.ID == "" {
		t.Fatal("score id empty")
	}

	batch := []byte(`{"scores":[
	  {"traceId":"trace_001","name":"verdict","value":1,"comment":"ok"},
	  {"traceId":"trace_001","name":"faithful","value":2,"comment":"ok"}]}`)
	r, err = http.Post(srv.URL+"/api/scores/batch", "application/json", bytes.NewReader(batch))
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	var batched struct {
		Count int      `json:"count"`
		IDs   []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&batched)
	r.Body.Close()
	if batched.Count != 2 || len(batched.IDs) != 2 {
		t.Fatalf("batch result = %+v", batched)
	}

	r, err = http.Post(srv.URL+"/api/judge", "application/json", bytes.NewReader([]byte(`{"traceId":"trace_001"}`)))
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("judge without config = %d, want 400", r.StatusCode)
	}
}
