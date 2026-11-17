package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Span is one row of the unified wide table.
type Span struct {
	ProjectID           string `json:"projectId"`
	SpanID              string `json:"spanId"`
	TraceID             string `json:"traceId"`
	ParentSpanID        string `json:"parentSpanId"`
	Type                string `json:"type"`
	Name                string `json:"name"`
	StartTime           int64  `json:"startTime"`
	EndTime             *int64 `json:"endTime,omitempty"`
	CompletionStartTime *int64 `json:"completionStartTime,omitempty"`
	SessionID           string `json:"sessionId,omitempty"`
	UserID              string `json:"userId,omitempty"`
	Environment         string `json:"environment"`
	Release             string `json:"release,omitempty"`
	Version             string `json:"version,omitempty"`
	Tags                string `json:"tags"`
	Level               string `json:"level"`
	StatusMessage       string `json:"statusMessage,omitempty"`
	Model               string `json:"model,omitempty"`
	ModelParameters     string `json:"modelParameters,omitempty"`
	PromptName          string `json:"promptName,omitempty"`
	PromptVersion       *int64 `json:"promptVersion,omitempty"`
	UsageDetails        string `json:"usageDetails,omitempty"`
	CostDetails         string `json:"costDetails,omitempty"`
	Input               string `json:"input,omitempty"`
	Output              string `json:"output,omitempty"`
	Metadata            string `json:"metadata,omitempty"`
	EventTS             int64  `json:"eventTs"`
	IsDeleted           int    `json:"-"`
	CreatedAt           int64  `json:"createdAt"`
	UpdatedAt           int64  `json:"updatedAt"`
}

type Score struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"projectId"`
	TraceID     string   `json:"traceId"`
	SpanID      string   `json:"spanId,omitempty"`
	Name        string   `json:"name"`
	DataType    string   `json:"dataType"`
	Value       *float64 `json:"value,omitempty"`
	StringValue string   `json:"stringValue,omitempty"`
	Comment     string   `json:"comment,omitempty"`
	Source      string   `json:"source"`
	AuthorName  string   `json:"authorName,omitempty"`
	// RawResponse matters only for source=LLM: the model's raw output, kept for audit.
	RawResponse string `json:"rawResponse,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

type TraceSummary struct {
	TraceID     string  `json:"traceId"`
	Name        string  `json:"name"`
	StartTime   int64   `json:"startTime"`
	EndTime     int64   `json:"endTime"`
	SpanCount   int64   `json:"spanCount"`
	Cost        float64 `json:"cost"`
	Tokens      int64   `json:"tokens"`
	HasError    bool    `json:"hasError"`
	Environment string  `json:"environment"`
}

type TraceFilter struct {
	ProjectID   string
	From, To    int64
	Name        string
	Q           string // full-text search over name, input and output
	UserID      string
	SessionID   string
	Level       string
	Environment string
	Limit       int
	CursorTS    int64
	CursorID    string
}

type Store struct {
	read  *sql.DB
	write *sql.DB
	path  string
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	path := filepath.Join(dataDir, "app.db")
	writeDSN := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)"
	w, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	readDSN := "file:" + path + "?mode=ro&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=cache_size(-64000)"
	r, err := sql.Open("sqlite", readDSN)
	if err != nil {
		return nil, err
	}
	r.SetMaxOpenConns(4)

	s := &Store{read: r, write: w, path: path}
	if err := s.migrate(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	_ = s.read.Close()
	return s.write.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.write.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists int
		if err := s.write.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.write.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := s.write.ExecContext(ctx,
			`INSERT INTO schema_migrations(name, applied_at) VALUES(?, ?)`,
			name, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

var upsertCols = []string{
	"project_id", "span_id", "trace_id", "parent_span_id", "type", "name",
	"start_time", "end_time", "completion_start_time", "session_id", "user_id",
	"environment", "release", "version", "tags", "level", "status_message",
	"model", "model_parameters", "prompt_name", "prompt_version",
	"usage_details", "cost_details", "input",
	"output", "metadata", "event_ts", "is_deleted", "created_at", "updated_at",
}

var caseMergeCols = []string{
	"parent_span_id", "type", "name", "session_id", "user_id", "environment",
	"release", "version", "tags", "level", "status_message", "model",
	"model_parameters", "prompt_name", "usage_details", "cost_details", "input", "output", "metadata",
}

func buildUpsertSQL(rows int) string {
	var b strings.Builder
	b.WriteString("INSERT INTO spans (")
	b.WriteString(strings.Join(upsertCols, ", "))
	b.WriteString(") VALUES ")
	ph := "(" + strings.Repeat("?, ", len(upsertCols)-1) + "?)"
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ph)
	}
	b.WriteString(" ON CONFLICT (project_id, trace_id, span_id) DO UPDATE SET ")
	sets := make([]string, 0, len(caseMergeCols)+4)
	for _, c := range caseMergeCols {
		sets = append(sets, fmt.Sprintf(
			"%s = CASE WHEN excluded.event_ts >= spans.event_ts AND excluded.%s <> '' THEN excluded.%s ELSE spans.%s END",
			c, c, c, c))
	}
	sets = append(sets,
		"end_time = CASE WHEN excluded.event_ts >= spans.event_ts AND excluded.end_time IS NOT NULL THEN excluded.end_time ELSE spans.end_time END",
		"completion_start_time = CASE WHEN excluded.event_ts >= spans.event_ts AND excluded.completion_start_time IS NOT NULL THEN excluded.completion_start_time ELSE spans.completion_start_time END",
		"prompt_version = CASE WHEN excluded.event_ts >= spans.event_ts AND excluded.prompt_version IS NOT NULL THEN excluded.prompt_version ELSE spans.prompt_version END",
		"event_ts = CASE WHEN excluded.event_ts >= spans.event_ts AND excluded.event_ts <> 0 THEN excluded.event_ts ELSE spans.event_ts END",
		"updated_at = excluded.updated_at",
	)
	b.WriteString(strings.Join(sets, ", "))
	return b.String()
}

// UpsertSpans writes one batch inside a single transaction.
func (s *Store) UpsertSpans(ctx context.Context, rows []Span) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const chunk = 250
	for start := 0; start < len(rows); start += chunk {
		end := start + chunk
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		args := make([]any, 0, len(batch)*len(upsertCols))
		for _, r := range batch {
			var endTime, completion any
			if r.EndTime != nil {
				endTime = *r.EndTime
			}
			if r.CompletionStartTime != nil {
				completion = *r.CompletionStartTime
			}
			var promptVersion any
			if r.PromptVersion != nil {
				promptVersion = *r.PromptVersion
			}
			args = append(args,
				r.ProjectID, r.SpanID, r.TraceID, r.ParentSpanID, r.Type, r.Name,
				r.StartTime, endTime, completion, r.SessionID, r.UserID,
				r.Environment, r.Release, r.Version, r.Tags, r.Level, r.StatusMessage,
				r.Model, r.ModelParameters, r.PromptName, promptVersion,
				r.UsageDetails, r.CostDetails, r.Input,
				r.Output, r.Metadata, r.EventTS, r.IsDeleted, r.CreatedAt, r.UpdatedAt,
			)
		}
		if _, err := tx.ExecContext(ctx, buildUpsertSQL(len(batch)), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ftsQuery wraps input in an FTS5 phrase query so operators such as AND or -
// are matched literally.
func ftsQuery(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// ListTraces returns trace summaries for the ledger.
// Queries of 3+ characters use the FTS5 trigram index; 1-2 characters fall back
// to LIKE because trigram cannot match shorter strings.
func (s *Store) ListTraces(ctx context.Context, f TraceFilter) ([]TraceSummary, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	var b strings.Builder
	b.WriteString(`SELECT trace_id, ts, end_time, span_count, cost, tokens, has_error, name, env FROM (`)
	b.WriteString(traceAggSelect)
	args := []any{f.ProjectID, f.From, f.To}
	if f.UserID != "" {
		b.WriteString(" AND s.user_id=?")
		args = append(args, f.UserID)
	}
	if f.SessionID != "" {
		b.WriteString(" AND s.session_id=?")
		args = append(args, f.SessionID)
	}
	if f.Level != "" {
		b.WriteString(" AND s.level=?")
		args = append(args, f.Level)
	}
	if f.Environment != "" {
		b.WriteString(" AND s.environment=?")
		args = append(args, f.Environment)
	}
	if f.Name != "" {
		b.WriteString(" AND s.name LIKE ?")
		args = append(args, "%"+f.Name+"%")
	}
	if f.Q != "" {
		if utf8.RuneCountInString(f.Q) >= 3 {
			b.WriteString(` AND s.rowid IN (SELECT rowid FROM span_fts WHERE span_fts MATCH ?)`)
			args = append(args, ftsQuery(f.Q))
		} else {
			b.WriteString(` AND (s.name LIKE ? OR s.input LIKE ? OR s.output LIKE ?)`)
			q := "%" + f.Q + "%"
			args = append(args, q, q, q)
		}
	}
	b.WriteString(traceAggTail)
	b.WriteString(`) t WHERE 1=1`)
	if f.CursorTS > 0 {
		b.WriteString(" AND (ts < ? OR (ts = ? AND trace_id < ?))")
		args = append(args, f.CursorTS, f.CursorTS, f.CursorID)
	}
	b.WriteString(" ORDER BY ts DESC, trace_id DESC LIMIT ?")
	args = append(args, f.Limit)

	rows, err := s.read.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TraceSummary, 0, f.Limit)
	for rows.Next() {
		var t TraceSummary
		var hasErr, env sql.NullString
		var endTime sql.NullInt64
		if err := rows.Scan(&t.TraceID, &t.StartTime, &endTime, &t.SpanCount,
			&t.Cost, &t.Tokens, &hasErr, &t.Name, &env); err != nil {
			return nil, err
		}
		if endTime.Valid {
			t.EndTime = endTime.Int64
		}
		t.HasError = hasErr.Valid && hasErr.String == "1"
		if env.Valid {
			t.Environment = env.String
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTrace(ctx context.Context, projectID, traceID string) ([]Span, error) {
	q := `SELECT project_id, span_id, trace_id, parent_span_id, type, name,
		start_time, end_time, completion_start_time, session_id, user_id,
		environment, release, version, tags, level, status_message, model,
		model_parameters, prompt_name, prompt_version, usage_details, cost_details, input, output, metadata,
		event_ts, is_deleted, created_at, updated_at
		FROM spans WHERE project_id=? AND trace_id=? AND is_deleted=0
		ORDER BY start_time ASC, span_id ASC`
	rows, err := s.read.QueryContext(ctx, q, projectID, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Span, 0, 64)
	for rows.Next() {
		var r Span
		var endTime, completion, promptVersion sql.NullInt64
		if err := rows.Scan(&r.ProjectID, &r.SpanID, &r.TraceID, &r.ParentSpanID,
			&r.Type, &r.Name, &r.StartTime, &endTime, &completion, &r.SessionID,
			&r.UserID, &r.Environment, &r.Release, &r.Version, &r.Tags, &r.Level,
			&r.StatusMessage, &r.Model, &r.ModelParameters, &r.PromptName, &promptVersion,
			&r.UsageDetails,
			&r.CostDetails, &r.Input, &r.Output, &r.Metadata, &r.EventTS,
			&r.IsDeleted, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if endTime.Valid {
			v := endTime.Int64
			r.EndTime = &v
		}
		if completion.Valid {
			v := completion.Int64
			r.CompletionStartTime = &v
		}
		if promptVersion.Valid {
			v := promptVersion.Int64
			r.PromptVersion = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AddScore(ctx context.Context, sc *Score) error {
	return s.AddScores(ctx, []*Score{sc})
}

// AddScores writes a batch of scores in one transaction: a batch verdict either
// lands in full or not at all, which keeps the queue counters consistent.
func (s *Store) AddScores(ctx context.Context, list []*Score) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UnixMilli()
	for _, sc := range list {
		if sc.ID == "" {
			sc.ID = "sc_" + randHex(12)
		}
		if sc.CreatedAt == 0 {
			sc.CreatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scores
			(id, project_id, trace_id, span_id, name, data_type, value, string_value, comment, source, author_name, raw_response, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			sc.ID, sc.ProjectID, sc.TraceID, sc.SpanID, sc.Name, sc.DataType,
			sc.Value, sc.StringValue, sc.Comment, sc.Source, sc.AuthorName,
			sc.RawResponse, sc.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListScores(ctx context.Context, projectID, traceID string) ([]Score, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id, project_id, trace_id, span_id,
		name, data_type, value, string_value, comment, source, author_name, raw_response, created_at
		FROM scores WHERE project_id=? AND trace_id=? ORDER BY created_at DESC`,
		projectID, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Score, 0, 16)
	for rows.Next() {
		var sc Score
		var v sql.NullFloat64
		if err := rows.Scan(&sc.ID, &sc.ProjectID, &sc.TraceID, &sc.SpanID, &sc.Name,
			&sc.DataType, &v, &sc.StringValue, &sc.Comment, &sc.Source,
			&sc.AuthorName, &sc.RawResponse, &sc.CreatedAt); err != nil {
			return nil, err
		}
		if v.Valid {
			f := v.Float64
			sc.Value = &f
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// CountLLMJudgementsSince counts model-written scores after since for the daily budget guard.
func (s *Store) CountLLMJudgementsSince(ctx context.Context, projectID string, since int64) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT count(*) FROM scores
		WHERE project_id=? AND source='LLM' AND created_at>=?`, projectID, since).Scan(&n)
	return n, err
}

// ProjectBySecret returns the project owning the given secret key.
func (s *Store) ProjectBySecret(ctx context.Context, secret string) (string, bool) {
	sum := sha256.Sum256([]byte(secret))
	h := hex.EncodeToString(sum[:])
	var projectID string
	err := s.read.QueryRowContext(ctx,
		`SELECT project_id FROM api_keys WHERE secret_hash=?`, h).Scan(&projectID)
	if err != nil {
		return "", false
	}
	_, _ = s.write.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at=? WHERE secret_hash=?`, time.Now().UnixMilli(), h)
	return projectID, true
}

// Bootstrap creates the default project and key on first start.
func (s *Store) Bootstrap(ctx context.Context) (projectID, pk, sk string, err error) {
	var count int
	if err = s.write.QueryRowContext(ctx, `SELECT COUNT(1) FROM projects`).Scan(&count); err != nil {
		return
	}
	if count > 0 {
		return "", "", "", nil
	}
	projectID = "prj_" + randHex(8)
	pk = "pk-trawl-" + randHex(12)
	sk = "sk-trawl-" + randHex(32)
	sum := sha256.Sum256([]byte(sk))
	now := time.Now().UnixMilli()
	tx, txErr := s.write.BeginTx(ctx, nil)
	if txErr != nil {
		return "", "", "", txErr
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO projects(id, name, created_at) VALUES(?,?,?)`,
		projectID, "default", now); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO api_keys(id, project_id, public_key, secret_hash, name, created_at)
		VALUES(?,?,?,?,?,?)`,
		"key_"+randHex(8), projectID, pk, hex.EncodeToString(sum[:]), "default", now); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	return projectID, pk, sk, nil
}

func (s *Store) DefaultProject(ctx context.Context) (string, error) {
	var id string
	err := s.read.QueryRowContext(ctx,
		`SELECT id FROM projects ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	return id, err
}

type Price struct {
	InputPer1k  float64
	OutputPer1k float64
}

func (s *Store) Prices(ctx context.Context) (map[string]Price, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT model, input_per_1k, output_per_1k FROM model_prices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Price{}
	for rows.Next() {
		var m string
		var p Price
		if err := rows.Scan(&m, &p.InputPer1k, &p.OutputPer1k); err != nil {
			return nil, err
		}
		out[m] = p
	}
	return out, rows.Err()
}

type QueueStats struct {
	Pending     int `json:"pending"`
	JudgedToday int `json:"judgedToday"`
	JudgedTotal int `json:"judgedTotal"`
}

// traceAggSelect is the SELECT and WHERE prefix of the trace aggregate; callers
// may append filters and close with traceAggTail.
const traceAggSelect = `SELECT s.trace_id AS trace_id,
	       MIN(s.start_time) AS ts,
	       MAX(COALESCE(s.end_time, s.start_time)) AS end_time,
	       SUM(CASE WHEN s.type='trace' THEN 0 ELSE 1 END) AS span_count,
	       SUM(COALESCE(CAST(json_extract(NULLIF(s.cost_details,''),'$.total') AS REAL),0)) AS cost,
	       SUM(COALESCE(CAST(json_extract(NULLIF(s.usage_details,''),'$.total') AS INTEGER),0)) AS tokens,
	       MAX(CASE WHEN s.level='ERROR' THEN 1 ELSE 0 END) AS has_error,
	       -- Prefer the trace row's name; bare OTLP falls back to the root span.
	       -- Keep this a pure aggregate: a correlated lookup per trace degenerates
	       -- into a full scan (25s at 10k traces).
	       COALESCE(
	         MAX(CASE WHEN s.type='trace' AND s.name<>'' THEN s.name END),
	         MAX(CASE WHEN s.parent_span_id='' AND s.name<>'' THEN s.name END),
	         ''
	       ) AS name,
	       MAX(s.environment) AS env,
	       s.project_id AS project_id
	FROM spans s
	WHERE s.project_id=? AND s.is_deleted=0 AND s.start_time>=? AND s.start_time<?`

// traceAggTail closes the aggregate; ListTraces inserts its filters before it.
const traceAggTail = `
	GROUP BY s.trace_id, s.project_id`

const traceAggSQL = traceAggSelect + traceAggTail

// ListQueue returns traces that still lack a verdict score, oldest first,
// together with the counters the queue header displays.
func (s *Store) ListQueue(ctx context.Context, projectID string, from, to int64, limit int) ([]TraceSummary, QueueStats, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	now := time.Now()
	if to <= 0 {
		to = now.UnixMilli() + 60_000
	}
	if from <= 0 {
		from = to - 7*24*3600_000
	}
	var stats QueueStats

	q := `SELECT t.trace_id, t.ts, t.end_time, t.span_count, t.cost, t.tokens, t.has_error, t.name, t.env
	FROM (` + traceAggSQL + `) t
	LEFT JOIN scores sc ON sc.project_id=t.project_id AND sc.trace_id=t.trace_id AND sc.name='verdict'
	WHERE sc.id IS NULL
	ORDER BY t.ts ASC, t.trace_id ASC
	LIMIT ?`
	rows, err := s.read.QueryContext(ctx, q, projectID, from, to, limit)
	if err != nil {
		return nil, stats, err
	}
	defer rows.Close()

	out := make([]TraceSummary, 0, limit)
	for rows.Next() {
		var t TraceSummary
		var hasErr, env sql.NullString
		var endTime sql.NullInt64
		if err := rows.Scan(&t.TraceID, &t.StartTime, &endTime, &t.SpanCount,
			&t.Cost, &t.Tokens, &hasErr, &t.Name, &env); err != nil {
			return nil, stats, err
		}
		if endTime.Valid {
			t.EndTime = endTime.Int64
		}
		t.HasError = hasErr.Valid && hasErr.String == "1"
		if env.Valid {
			t.Environment = env.String
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, stats, err
	}

	countSQL := `SELECT COUNT(*) FROM (` + traceAggSQL + `) t
	LEFT JOIN scores sc ON sc.project_id=t.project_id AND sc.trace_id=t.trace_id AND sc.name='verdict'
	WHERE sc.id IS NULL`
	if err := s.read.QueryRowContext(ctx, countSQL, projectID, from, to).Scan(&stats.Pending); err != nil {
		return nil, stats, err
	}

	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	if err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT trace_id) FROM scores
		 WHERE project_id=? AND name='verdict' AND created_at>=?`,
		projectID, todayStart).Scan(&stats.JudgedToday); err != nil {
		return nil, stats, err
	}
	if err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT trace_id) FROM scores WHERE project_id=? AND name='verdict'`,
		projectID).Scan(&stats.JudgedTotal); err != nil {
		return nil, stats, err
	}
	return out, stats, nil
}

type PromptStat struct {
	Name    string  `json:"name"`
	Version int64   `json:"version"`
	Calls   int64   `json:"calls"`
	Tokens  int64   `json:"tokens"`
	Cost    float64 `json:"cost"`
	Judged  int64   `json:"judged"`
	Passed  int64   `json:"passed"`
}

// ListPrompts aggregates generations by prompt name + version, with verdict
// pass rates joined through the trace. Small windows keep the EXISTS lookups
// cheap; a materialised table can replace it later if needed.
func (s *Store) ListPrompts(ctx context.Context, projectID string, from, to int64) ([]PromptStat, error) {
	now := time.Now()
	if to <= 0 {
		to = now.UnixMilli() + 60_000
	}
	if from <= 0 {
		from = to - 30*24*3600_000
	}
	q := `SELECT s.prompt_name,
	       COALESCE(s.prompt_version,0) AS version,
	       COUNT(*) AS calls,
	       SUM(COALESCE(CAST(json_extract(NULLIF(s.usage_details,''),'$.total') AS INTEGER),0)) AS tokens,
	       SUM(COALESCE(CAST(json_extract(NULLIF(s.cost_details,''),'$.total') AS REAL),0)) AS cost,
	       SUM(CASE WHEN EXISTS(SELECT 1 FROM scores sc
	             WHERE sc.project_id=s.project_id AND sc.trace_id=s.trace_id AND sc.name='verdict')
	           THEN 1 ELSE 0 END) AS judged,
	       SUM(CASE WHEN EXISTS(SELECT 1 FROM scores sc
	             WHERE sc.project_id=s.project_id AND sc.trace_id=s.trace_id
	               AND sc.name='verdict' AND sc.value IS NOT NULL AND sc.value >= 1)
	           THEN 1 ELSE 0 END) AS passed
	FROM spans s
	WHERE s.project_id=? AND s.is_deleted=0 AND s.prompt_name<>''
	  AND s.start_time>=? AND s.start_time<?
	GROUP BY s.prompt_name, COALESCE(s.prompt_version,0)
	ORDER BY calls DESC, s.prompt_name ASC`
	rows, err := s.read.QueryContext(ctx, q, projectID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PromptStat, 0, 32)
	for rows.Next() {
		var p PromptStat
		if err := rows.Scan(&p.Name, &p.Version, &p.Calls, &p.Tokens, &p.Cost, &p.Judged, &p.Passed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
