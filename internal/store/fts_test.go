package store

import (
	"context"
	"testing"
)

// TestFTS5Support probes FTS5 availability and Chinese searchability.
// Finding: unicode61 treats a whole Chinese sentence as one token, so
// two-character words never match; trigram handles 3+ characters only.
func TestFTS5Support(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	wdb := st.write

	if _, err := wdb.ExecContext(ctx, `CREATE VIRTUAL TABLE fts_probe USING fts5(body, tokenize='unicode61')`); err != nil {
		t.Skipf("fts5 not compiled into driver: %v", err)
	}
	t.Log("fts5 + unicode61: available")

	if _, err := wdb.ExecContext(ctx, `INSERT INTO fts_probe(body) VALUES ('退款七个工作日原路退回')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var hits int
	if err := wdb.QueryRowContext(ctx, `SELECT count(*) FROM fts_probe WHERE fts_probe MATCH '原路退回'`).Scan(&hits); err != nil {
		t.Fatalf("match: %v", err)
	}
	t.Logf("unicode61 phrase hits = %d (two-character substrings cannot match: the whole sentence is one token)", hits)

	if _, err := wdb.ExecContext(ctx, `CREATE VIRTUAL TABLE fts_tri USING fts5(body, tokenize='trigram')`); err != nil {
		t.Logf("trigram tokenizer unavailable: %v", err)
	} else {
		if _, err := wdb.ExecContext(ctx, `INSERT INTO fts_tri(body) VALUES ('退款七个工作日原路退回')`); err != nil {
			t.Fatalf("trigram insert: %v", err)
		}
		var tri int
		if err := wdb.QueryRowContext(ctx, `SELECT count(*) FROM fts_tri WHERE fts_tri MATCH '七个工'`).Scan(&tri); err != nil {
			t.Fatalf("trigram match: %v", err)
		}
		t.Logf("trigram 3+ character substring hits = %d", tri)
		_, _ = wdb.ExecContext(ctx, `DROP TABLE fts_tri`)
	}

	_, _ = wdb.ExecContext(ctx, `DROP TABLE fts_probe`)
}
