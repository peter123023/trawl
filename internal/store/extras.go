package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Datasets

type Dataset struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Note      string `json:"note"`
	Items     int64  `json:"items"`
	CreatedAt int64  `json:"createdAt"`
}

// DatasetItem joins a dataset entry with its trace summary for the UI table.
type DatasetItem struct {
	ID            string   `json:"id"`
	TraceID       string   `json:"traceId"`
	Note          string   `json:"note"`
	CreatedAt     int64    `json:"createdAt"`
	TraceName     string   `json:"traceName"`
	StartTime     int64    `json:"startTime"`
	PromptName    string   `json:"promptName,omitempty"`
	PromptVersion *int64   `json:"promptVersion,omitempty"`
	Verdict       *float64 `json:"verdict,omitempty"`
}

// VersionStat is a pass-rate slice by prompt version.
type VersionStat struct {
	PromptName    string   `json:"promptName"`
	PromptVersion *int64   `json:"promptVersion,omitempty"`
	Items         int64    `json:"items"`
	Judged        int64    `json:"judged"`
	Passed        int64    `json:"passed"`
	PassRate      float64  `json:"passRate"`
	AvgVerdict    *float64 `json:"avgVerdict,omitempty"`
}

func (s *Store) ListDatasets(ctx context.Context, projectID string) ([]Dataset, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT d.id, d.project_id, d.name, d.note, d.created_at,
		(SELECT COUNT(1) FROM dataset_items i WHERE i.dataset_id = d.id) AS items
		FROM datasets d WHERE d.project_id=? ORDER BY d.created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Dataset, 0, 16)
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Note, &d.CreatedAt, &d.Items); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateDataset(ctx context.Context, projectID, name, note string) (*Dataset, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dataset name is required")
	}
	now := time.Now().UnixMilli()
	d := &Dataset{
		ID: "ds_" + randHex(10), ProjectID: projectID,
		Name: strings.TrimSpace(name), Note: strings.TrimSpace(note), CreatedAt: now,
	}
	if _, err := s.write.ExecContext(ctx, `INSERT INTO datasets(id, project_id, name, note, created_at)
		VALUES(?,?,?,?,?)`, d.ID, projectID, d.Name, d.Note, now); err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteDataset reports whether the dataset existed, so callers can answer 404 or 200.
func (s *Store) DeleteDataset(ctx context.Context, projectID, id string) (bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM dataset_items WHERE project_id=? AND dataset_id=?`, projectID, id); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM datasets WHERE project_id=? AND id=?`, projectID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// AddDatasetItems adds traces to a dataset; duplicates are skipped by the unique index.
func (s *Store) AddDatasetItems(ctx context.Context, projectID, datasetID string, traceIDs []string, note string) (int, error) {
	var exists int
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM datasets WHERE project_id=? AND id=?`,
		projectID, datasetID).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, fmt.Errorf("dataset not found")
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	added := 0
	for _, id := range traceIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO dataset_items(id, dataset_id, project_id, trace_id, note, created_at)
			VALUES(?,?,?,?,?,?) ON CONFLICT(dataset_id, trace_id) DO NOTHING`,
			"di_"+randHex(10), datasetID, projectID, id, note, now)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, tx.Commit()
}

func (s *Store) RemoveDatasetItem(ctx context.Context, projectID, datasetID, traceID string) error {
	_, err := s.write.ExecContext(ctx, `DELETE FROM dataset_items
		WHERE project_id=? AND dataset_id=? AND trace_id=?`, projectID, datasetID, traceID)
	return err
}

func (s *Store) DatasetItems(ctx context.Context, projectID, datasetID string) ([]DatasetItem, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT i.id, i.trace_id, i.note, i.created_at,
		COALESCE((SELECT COALESCE(NULLIF(s2.name,''),'') FROM spans s2
		           WHERE s2.project_id=i.project_id AND s2.trace_id=i.trace_id
		             AND (s2.parent_span_id='' OR s2.type='trace')
		           ORDER BY s2.start_time ASC LIMIT 1), ''),
		COALESCE((SELECT MIN(s2.start_time) FROM spans s2
		           WHERE s2.project_id=i.project_id AND s2.trace_id=i.trace_id), 0),
		COALESCE((SELECT MAX(s2.prompt_name) FROM spans s2
		           WHERE s2.project_id=i.project_id AND s2.trace_id=i.trace_id), ''),
		(SELECT MAX(s2.prompt_version) FROM spans s2
		   WHERE s2.project_id=i.project_id AND s2.trace_id=i.trace_id),
		(SELECT MAX(sc.value) FROM scores sc
		   WHERE sc.project_id=i.project_id AND sc.trace_id=i.trace_id AND sc.name='verdict')
		FROM dataset_items i
		WHERE i.project_id=? AND i.dataset_id=?
		ORDER BY i.created_at DESC`, projectID, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DatasetItem, 0, 64)
	for rows.Next() {
		var it DatasetItem
		var ver, pver sql.NullInt64
		var verdict sql.NullFloat64
		if err := rows.Scan(&it.ID, &it.TraceID, &it.Note, &it.CreatedAt,
			&it.TraceName, &it.StartTime, &it.PromptName, &pver, &verdict); err != nil {
			return nil, err
		}
		if pver.Valid {
			v := pver.Int64
			it.PromptVersion = &v
		}
		_ = ver
		if verdict.Valid {
			f := verdict.Float64
			it.Verdict = &f
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// CompareDataset slices pass rates by prompt version to answer whether v2 beats v1.
func (s *Store) CompareDataset(ctx context.Context, projectID, datasetID string) ([]VersionStat, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT
		CASE WHEN COALESCE(pname,'')='' THEN '（无 prompt 标记）' ELSE pname END AS label,
		pver,
		COUNT(DISTINCT d.trace_id) AS items,
		COUNT(DISTINCT CASE WHEN v IS NOT NULL THEN d.trace_id END) AS judged,
		COUNT(DISTINCT CASE WHEN v >= 1 THEN d.trace_id END) AS passed,
		AVG(v) AS avg_v
		FROM (
			SELECT d.trace_id,
			       (SELECT MAX(s.prompt_name) FROM spans s
			         WHERE s.project_id=d.project_id AND s.trace_id=d.trace_id) AS pname,
			       (SELECT MAX(s.prompt_version) FROM spans s
			         WHERE s.project_id=d.project_id AND s.trace_id=d.trace_id) AS pver,
			       (SELECT MAX(sc.value) FROM scores sc
			         WHERE sc.project_id=d.project_id AND sc.trace_id=d.trace_id AND sc.name='verdict') AS v
			FROM dataset_items d
			WHERE d.project_id=? AND d.dataset_id=?
		) d
		GROUP BY label, pver
		ORDER BY pver DESC, label`, projectID, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]VersionStat, 0, 8)
	for rows.Next() {
		var st VersionStat
		var pver sql.NullInt64
		var avg sql.NullFloat64
		if err := rows.Scan(&st.PromptName, &pver, &st.Items, &st.Judged, &st.Passed, &avg); err != nil {
			return nil, err
		}
		if pver.Valid {
			v := pver.Int64
			st.PromptVersion = &v
		}
		if avg.Valid {
			f := avg.Float64
			st.AvgVerdict = &f
		}
		if st.Items > 0 {
			st.PassRate = float64(st.Passed) / float64(st.Items) * 100
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Model prices

type PriceRow struct {
	Model           string   `json:"model"`
	InputPer1k      float64  `json:"inputPer1k"`
	OutputPer1k     float64  `json:"outputPer1k"`
	CacheReadPer1k  *float64 `json:"cacheReadPer1k,omitempty"`
	UpdatedAt       int64    `json:"updatedAt"`
	UnknownInPrices bool     `json:"-"`
}

func (s *Store) ListPrices(ctx context.Context) ([]PriceRow, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT model, input_per_1k, output_per_1k, cache_read_per_1k, updated_at
		FROM model_prices ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PriceRow, 0, 64)
	for rows.Next() {
		var p PriceRow
		var cache sql.NullFloat64
		if err := rows.Scan(&p.Model, &p.InputPer1k, &p.OutputPer1k, &cache, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if cache.Valid {
			c := cache.Float64
			p.CacheReadPer1k = &c
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpsertPrices(ctx context.Context, list []PriceRow) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	n := 0
	for _, p := range list {
		if strings.TrimSpace(p.Model) == "" || p.InputPer1k < 0 || p.OutputPer1k < 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_prices(model, input_per_1k, output_per_1k, cache_read_per_1k, updated_at)
			VALUES(?,?,?,?,?) ON CONFLICT(model) DO UPDATE SET
			input_per_1k=excluded.input_per_1k,
			output_per_1k=excluded.output_per_1k,
			cache_read_per_1k=excluded.cache_read_per_1k,
			updated_at=excluded.updated_at`,
			p.Model, p.InputPer1k, p.OutputPer1k, p.CacheReadPer1k, now); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

func (s *Store) DeletePrice(ctx context.Context, model string) error {
	_, err := s.write.ExecContext(ctx, `DELETE FROM model_prices WHERE model=?`, model)
	return err
}

// Backup

// BackupTo writes a consistent snapshot with VACUUM INTO, with no write pause
// and no -wal copy.
func (s *Store) BackupTo(ctx context.Context, dir string) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	name := fmt.Sprintf("trawl-%s.db", time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if _, err := s.write.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return "", 0, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return path, st.Size(), nil
}

// Projects

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	Traces    int64  `json:"traces"`
	Keys      int64  `json:"keys"`
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT p.id, p.name, p.created_at,
		(SELECT COUNT(DISTINCT sp.trace_id) FROM spans sp WHERE sp.project_id=p.id),
		(SELECT COUNT(1) FROM api_keys k WHERE k.project_id=p.id)
		FROM projects p ORDER BY p.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Project, 0, 8)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Traces, &p.Keys); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProjectExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM projects WHERE id=?`, id).Scan(&n)
	return n > 0, err
}

// CreateProject creates a project and issues a key pair; the secret appears
// only here because the store keeps hashes.
func (s *Store) CreateProject(ctx context.Context, name string) (projectID, pk, sk string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", "", fmt.Errorf("project name is required")
	}
	projectID = "prj_" + randHex(8)
	pk = "pk-trawl-" + randHex(12)
	sk = "sk-trawl-" + randHex(32)
	sum := sha256.Sum256([]byte(sk))
	now := time.Now().UnixMilli()

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO projects(id, name, created_at) VALUES(?,?,?)`,
		projectID, name, now); err != nil {
		return "", "", "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO api_keys(id, project_id, public_key, secret_hash, name, created_at)
		VALUES(?,?,?,?,?,?)`,
		"key_"+randHex(8), projectID, pk, hex.EncodeToString(sum[:]), "default", now); err != nil {
		return "", "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", "", err
	}
	return projectID, pk, sk, nil
}

// ExportScores exports verdicts as CSV; from/to of 0 mean unbounded.
func (s *Store) ExportScores(ctx context.Context, projectID string, from, to int64) ([]Score, error) {
	query := `SELECT id, project_id, trace_id, span_id, name, data_type, value, string_value,
		comment, source, author_name, raw_response, created_at
		FROM scores WHERE project_id=?`
	args := []any{projectID}
	if from > 0 {
		query += ` AND created_at >= ?`
		args = append(args, from)
	}
	if to > 0 {
		query += ` AND created_at < ?`
		args = append(args, to)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Score, 0, 256)
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
