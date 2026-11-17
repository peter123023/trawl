package store

import (
	"context"
	"testing"
	"time"
)

func TestUpsertMergeAndQuery(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	projectID, _, _, err := st.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if projectID == "" {
		t.Fatal("bootstrap returned empty project")
	}

	end := int64(1794837600500)
	first := []Span{{
		ProjectID: projectID, SpanID: "gen_1", TraceID: "trace_1",
		Type: "generation", Name: "answer", StartTime: 1794837600000,
		EndTime: &end, Input: "q", Output: "v1", Model: "gpt-4o-mini",
		UsageDetails: `{"input":100,"output":20,"total":120}`,
		CostDetails:  `{"input":0.000015,"output":0.000012,"total":0.000027}`,
		EventTS:      1, CreatedAt: 1, UpdatedAt: 1, Environment: "default", Level: "DEFAULT", Tags: "[]",
	}}
	if err := st.UpsertSpans(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := first[0]
	second.Output = "v2"
	second.EventTS = 2
	second.UpdatedAt = 2
	second.Input = ""
	if err := st.UpsertSpans(ctx, []Span{second}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	spans, err := st.GetTrace(ctx, projectID, "trace_1")
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	if spans[0].Output != "v2" {
		t.Errorf("output not merged: %q", spans[0].Output)
	}
	if spans[0].Input != "q" {
		t.Errorf("older empty field clobbered newer value: %q", spans[0].Input)
	}

	stale := second
	stale.EventTS = 1
	stale.Output = "stale"
	if err := st.UpsertSpans(ctx, []Span{stale}); err != nil {
		t.Fatalf("stale upsert: %v", err)
	}
	spans, _ = st.GetTrace(ctx, projectID, "trace_1")
	if spans[0].Output != "v2" {
		t.Errorf("stale event overwrote: %q", spans[0].Output)
	}

	traces, err := st.ListTraces(ctx, TraceFilter{
		ProjectID: projectID, From: 1794837000000, To: 1794838000000, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(traces) != 1 || traces[0].SpanCount != 1 {
		t.Fatalf("traces = %+v", traces)
	}
	if traces[0].Tokens != 120 {
		t.Errorf("tokens = %d", traces[0].Tokens)
	}

	sc := &Score{ProjectID: projectID, TraceID: "trace_1", Name: "faithful", Value: ptr(2.0)}
	if err := st.AddScore(ctx, sc); err != nil {
		t.Fatalf("add score: %v", err)
	}
	if sc.ID == "" {
		t.Error("score id not assigned")
	}
	scores, err := st.ListScores(ctx, projectID, "trace_1")
	if err != nil || len(scores) != 1 {
		t.Fatalf("scores: %v %v", scores, err)
	}
}

func TestProjectBySecret(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	_, _, sk, err := st.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if sk == "" {
		t.Fatal("no secret generated")
	}
	if _, ok := st.ProjectBySecret(ctx, "sk-trawl-wrong"); ok {
		t.Error("wrong secret accepted")
	}
	if _, ok := st.ProjectBySecret(ctx, sk); !ok {
		t.Error("valid secret rejected")
	}
}

func TestQueueExcludesJudged(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	projectID, _, _, err := st.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	now := time.Now().UnixMilli()
	end := now + 120
	span := Span{
		ProjectID: projectID, SpanID: "sq1", TraceID: "trace_q",
		Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
		EventTS: now, CreatedAt: now, UpdatedAt: now,
		Environment: "default", Level: "DEFAULT", Tags: "[]",
	}
	if err := st.UpsertSpans(ctx, []Span{span}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	items, stats, err := st.ListQueue(ctx, projectID, 0, 0, 10)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(items) != 1 || stats.Pending != 1 {
		t.Fatalf("want 1 pending, got %d items / pending %d", len(items), stats.Pending)
	}

	sc := &Score{ProjectID: projectID, TraceID: "trace_q", Name: "verdict", Value: ptr(1.0)}
	if err := st.AddScore(ctx, sc); err != nil {
		t.Fatalf("score: %v", err)
	}

	items, stats, err = st.ListQueue(ctx, projectID, 0, 0, 10)
	if err != nil {
		t.Fatalf("queue after verdict: %v", err)
	}
	if len(items) != 0 || stats.Pending != 0 {
		t.Fatalf("verdict did not clear queue: %d items / pending %d", len(items), stats.Pending)
	}
	if stats.JudgedToday != 1 || stats.JudgedTotal != 1 {
		t.Fatalf("judged counters wrong: %+v", stats)
	}
}

func TestPromptAggregation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	projectID, _, _, err := st.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	now := time.Now().UnixMilli()
	end := now + 100
	mk := func(spanID, traceID string, version int64) Span {
		v := version
		return Span{
			ProjectID: projectID, SpanID: spanID, TraceID: traceID,
			Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
			PromptName: "refund-policy", PromptVersion: &v,
			UsageDetails: `{"input":100,"output":20,"total":120}`,
			CostDetails:  `{"input":0.000015,"output":0.000012,"total":0.000027}`,
			EventTS:      now, CreatedAt: now, UpdatedAt: now,
			Environment: "default", Level: "DEFAULT", Tags: "[]",
		}
	}
	if err := st.UpsertSpans(ctx, []Span{
		mk("s_v1", "trace_v1", 1),
		mk("s_v2", "trace_v2", 2),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	prompts, err := st.ListPrompts(ctx, projectID, 0, 0)
	if err != nil {
		t.Fatalf("prompts: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("want 2 prompt versions, got %d", len(prompts))
	}
	for _, p := range prompts {
		if p.Calls != 1 || p.Tokens != 120 || p.Judged != 0 {
			t.Errorf("unexpected stats: %+v", p)
		}
	}

	if err := st.AddScore(ctx, &Score{
		ProjectID: projectID, TraceID: "trace_v2", Name: "verdict", Value: ptr(1.0),
	}); err != nil {
		t.Fatalf("score: %v", err)
	}
	prompts, _ = st.ListPrompts(ctx, projectID, 0, 0)
	for _, p := range prompts {
		if p.Version == 2 && (p.Judged != 1 || p.Passed != 1) {
			t.Errorf("v2 verdict missing: %+v", p)
		}
		if p.Version == 1 && p.Judged != 0 {
			t.Errorf("v1 should be unjudged: %+v", p)
		}
	}
}

func ptr(f float64) *float64 { return &f }

// TestSpanIdentityIsTraceScoped pins OTLP semantics: span ids are unique only
// within their own trace. A key of (project_id, span_id) silently merged a
// same-named span from another trace into the first one.
func TestSpanIdentityIsTraceScoped(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	rows := []Span{
		{ProjectID: "prj_x", SpanID: "span_dup", TraceID: "trace_a", Type: "span",
			Name: "first", StartTime: now, EventTS: now, CreatedAt: now, UpdatedAt: now},
		{ProjectID: "prj_x", SpanID: "span_dup", TraceID: "trace_b", Type: "span",
			Name: "second", StartTime: now, EventTS: now, CreatedAt: now, UpdatedAt: now},
	}
	if err := st.UpsertSpans(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, tc := range []struct{ trace, name string }{{"trace_a", "first"}, {"trace_b", "second"}} {
		spans, err := st.GetTrace(ctx, "prj_x", tc.trace)
		if err != nil {
			t.Fatalf("get %s: %v", tc.trace, err)
		}
		if len(spans) != 1 {
			t.Fatalf("%s spans = %d, want 1", tc.trace, len(spans))
		}
		if spans[0].Name != tc.name {
			t.Errorf("%s name = %q, want %q", tc.trace, spans[0].Name, tc.name)
		}
	}
}

// TestTraceNamePrefersTraceRow checks that the trace row's name wins and bare
// OTLP falls back to the root span, without any per-trace correlated subquery.
func TestTraceNamePrefersTraceRow(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	rows := []Span{
		{ProjectID: "prj_x", SpanID: "root_early", TraceID: "trace_a", Type: "span",
			Name: "root_span_name", StartTime: now - 1000, EventTS: now, CreatedAt: now, UpdatedAt: now},
		{ProjectID: "prj_x", SpanID: "t_a", TraceID: "trace_a", Type: "trace",
			Name: "trace_row_name", StartTime: now, EventTS: now, CreatedAt: now, UpdatedAt: now},
		{ProjectID: "prj_x", SpanID: "root_b", TraceID: "trace_b", Type: "span",
			Name: "otlp_root_name", StartTime: now, EventTS: now, CreatedAt: now, UpdatedAt: now},
	}
	if err := st.UpsertSpans(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := st.ListTraces(ctx, TraceFilter{ProjectID: "prj_x", From: now - 5000, To: now + 5000, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]string{}
	for _, tr := range got {
		names[tr.TraceID] = tr.Name
	}
	if names["trace_a"] != "trace_row_name" {
		t.Errorf("trace_a name = %q, want trace_row_name", names["trace_a"])
	}
	if names["trace_b"] != "otlp_root_name" {
		t.Errorf("trace_b name = %q, want otlp_root_name", names["trace_b"])
	}
}
