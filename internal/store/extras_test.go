package store

import (
	"context"
	"testing"
)

// TestSearchUsesFTSIndex verifies that 3+ character queries use the trigram index
// while 2-character queries still match through LIKE.
func TestSearchTrigramAndShortQuery(t *testing.T) {
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

	now := int64(1794837600000)
	end := now + 100
	rows := []Span{
		{
			ProjectID: projectID, SpanID: "g1", TraceID: "tr_refund",
			Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
			Input: "退款多久到账？", Output: "七个工作日原路退回，银行卡可能延迟。",
			EventTS: now, CreatedAt: now, UpdatedAt: now,
			Environment: "default", Level: "DEFAULT", Tags: "[]",
		},
		{
			ProjectID: projectID, SpanID: "g2", TraceID: "tr_other",
			Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
			Input: "怎么修改密码？", Output: "在设置页点击重置。",
			EventTS: now, CreatedAt: now, UpdatedAt: now,
			Environment: "default", Level: "DEFAULT", Tags: "[]",
		},
	}
	if err := st.UpsertSpans(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	base := TraceFilter{ProjectID: projectID, From: now - 1000, To: now + 1000, Limit: 10}

	long := base
	long.Q = "工作日"
	got, err := st.ListTraces(ctx, long)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if len(got) != 1 || got[0].TraceID != "tr_refund" {
		t.Fatalf("fts search = %+v", got)
	}

	short := base
	short.Q = "退款"
	got, err = st.ListTraces(ctx, short)
	if err != nil {
		t.Fatalf("like search: %v", err)
	}
	if len(got) != 1 || got[0].TraceID != "tr_refund" {
		t.Fatalf("short search = %+v", got)
	}

	newOut := "已更新：三个工作日到账。"
	if err := st.UpsertSpans(ctx, []Span{{
		ProjectID: projectID, SpanID: "g1", TraceID: "tr_refund",
		Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
		Input: "退款多久到账？", Output: newOut,
		EventTS: now + 500, CreatedAt: now, UpdatedAt: now + 500,
		Environment: "default", Level: "DEFAULT", Tags: "[]",
	}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	upd := base
	upd.Q = "已更新"
	got, err = st.ListTraces(ctx, upd)
	if err != nil {
		t.Fatalf("after update: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("trigger did not refresh index: %+v", got)
	}
}

func TestDatasetCompare(t *testing.T) {
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

	now := int64(1794837600000)
	end := now + 100
	mk := func(spanID, traceID string, version int64) Span {
		v := version
		return Span{
			ProjectID: projectID, SpanID: spanID, TraceID: traceID,
			Type: "generation", Name: "answer", StartTime: now, EndTime: &end,
			PromptName: "refund-policy", PromptVersion: &v,
			Input: "q", Output: "a",
			EventTS: now, CreatedAt: now, UpdatedAt: now,
			Environment: "default", Level: "DEFAULT", Tags: "[]",
		}
	}
	if err := st.UpsertSpans(ctx, []Span{
		mk("g1", "t_v1", 1), mk("g2", "t_v2a", 2), mk("g3", "t_v2b", 2),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ds, err := st.CreateDataset(ctx, projectID, "退款回归", "两周一次的回归集")
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	added, err := st.AddDatasetItems(ctx, projectID, ds.ID, []string{"t_v1", "t_v2a", "t_v2b", "t_v1"}, "")
	if err != nil {
		t.Fatalf("add items: %v", err)
	}
	if added != 3 {
		t.Errorf("added = %d, want 3 (dedupe by unique index)", added)
	}

	if err := st.AddScores(ctx, []*Score{
		{ProjectID: projectID, TraceID: "t_v1", Name: "verdict", Value: ptr(0.0)},
		{ProjectID: projectID, TraceID: "t_v2a", Name: "verdict", Value: ptr(1.0)},
		{ProjectID: projectID, TraceID: "t_v2b", Name: "verdict", Value: ptr(1.0)},
	}); err != nil {
		t.Fatalf("scores: %v", err)
	}

	stats, err := st.CompareDataset(ctx, projectID, ds.ID)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("want 2 versions, got %+v", stats)
	}
	byVersion := map[int64]VersionStat{}
	for _, s := range stats {
		if s.PromptVersion != nil {
			byVersion[*s.PromptVersion] = s
		}
	}
	if byVersion[2].PassRate != 100 || byVersion[1].PassRate != 0 {
		t.Errorf("pass rates = v1 %.0f%% v2 %.0f%%", byVersion[1].PassRate, byVersion[2].PassRate)
	}

	items, err := st.DatasetItems(ctx, projectID, ds.ID)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("items = %d", len(items))
	}
}

func TestBackupAndPrices(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	backupDir := t.TempDir()
	path, size, err := st.BackupTo(ctx, backupDir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if size == 0 {
		t.Fatalf("backup empty: %s", path)
	}
	restored, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer restored.Close()
	if _, err := restored.write.ExecContext(ctx, `ATTACH DATABASE ? AS bak`, path); err != nil {
		t.Fatalf("attach: %v", err)
	}
	var tables int
	if err := restored.write.QueryRowContext(ctx, `SELECT COUNT(1) FROM bak.sqlite_master WHERE type='table'`).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tables == 0 {
		t.Fatal("backup has no tables")
	}

	n, err := st.UpsertPrices(ctx, []PriceRow{
		{Model: "gpt-4o-mini", InputPer1k: 0.00015, OutputPer1k: 0.0006},
		{Model: "claude-sonnet", InputPer1k: 0.003, OutputPer1k: 0.015},
		{Model: "", InputPer1k: 1},
	})
	if err != nil {
		t.Fatalf("prices: %v", err)
	}
	if n != 2 {
		t.Errorf("upserted = %d, want 2 (empty model skipped)", n)
	}
	prices, err := st.ListPrices(ctx)
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if len(prices) == 0 {
		t.Fatal("no prices")
	}
	if _, err := st.UpsertPrices(ctx, []PriceRow{{Model: "gpt-4o-mini", InputPer1k: 0.0002, OutputPer1k: 0.0007}}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	all, _ := st.ListPrices(ctx)
	var hit *PriceRow
	for i := range all {
		if all[i].Model == "gpt-4o-mini" {
			hit = &all[i]
		}
	}
	if hit == nil || hit.InputPer1k != 0.0002 {
		t.Errorf("overwrite failed: %+v", hit)
	}
}
