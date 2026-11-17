package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trawlhq/trawl/internal/judge"
	"github.com/trawlhq/trawl/internal/store"
	"github.com/trawlhq/trawl/internal/writer"
)

func TestJudgeBatchWithStub(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"score":2,"reason":"基本正确"}`}},
			},
		})
	}))
	defer stub.Close()

	jc, err := judge.New(judge.Config{BaseURL: stub.URL, APIKey: "test", Model: "stub", AllowPrivate: true})
	if err != nil {
		t.Fatalf("judge client: %v", err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	projectID, _, _, err := st.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	wr := writer.New(st, 64)
	api := New(st, wr, Options{ProjectID: projectID, Judge: jc, JudgeCap: 2})
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	now := time.Now().UnixMilli()
	end := now + 50
	for _, id := range []string{"trace_a", "trace_b", "trace_c"} {
		span := store.Span{
			ProjectID: projectID, SpanID: "gen_" + id, TraceID: id,
			Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
			Input: "退款多久到账？", Output: "7 个工作日原路退回。",
			EventTS: now, CreatedAt: now, UpdatedAt: now,
			Environment: "default", Level: "DEFAULT", Tags: "[]",
		}
		if err := st.UpsertSpans(ctx, []store.Span{span}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	type batchOut struct {
		OK        int              `json:"ok"`
		Failed    int              `json:"failed"`
		Truncated int              `json:"truncated"`
		Model     string           `json:"model"`
		Results   []map[string]any `json:"results"`
	}
	resp, err := http.Post(srv.URL+"/api/judge/batch", "application/json",
		bytes.NewReader([]byte(`{"traceIds":["trace_a","trace_b","trace_c"]}`)))
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	var out batchOut
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.OK != 2 || out.Failed != 0 || out.Truncated != 1 {
		t.Fatalf("batch result = %+v", out)
	}
	if out.Model != "stub" {
		t.Errorf("model = %q", out.Model)
	}

	scores, err := st.ListScores(ctx, projectID, "trace_a")
	if err != nil {
		t.Fatalf("scores: %v", err)
	}
	var judged bool
	for _, sc := range scores {
		if sc.Name == "llm_judge" && sc.RawResponse != "" && sc.Source == "LLM" {
			judged = true
		}
	}
	if !judged {
		t.Fatalf("llm_judge score with raw_response not found: %+v", scores)
	}

	resp, err = http.Post(srv.URL+"/api/judge", "application/json",
		bytes.NewReader([]byte(`{"traceId":"trace_c"}`)))
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("budget guard = %d, want 429", resp.StatusCode)
	}
}
