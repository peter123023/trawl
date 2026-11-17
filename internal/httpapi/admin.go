package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trawlhq/trawl/internal/judge"
	"github.com/trawlhq/trawl/internal/store"
)

// proj resolves the project for this request: an explicit header or query wins,
// and any unknown id falls back to the bootstrap project.
func (a *API) proj(r *http.Request) string {
	for _, id := range []string{
		r.Header.Get("X-Trawl-Project"),
		r.URL.Query().Get("project"),
	} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if ok, err := a.st.ProjectExists(r.Context(), id); err == nil && ok {
			return id
		}
	}
	return a.projectID
}

// Datasets

func (a *API) handleListDatasets(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListDatasets(r.Context(), a.proj(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": list})
}

func (a *API) handleCreateDataset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "json body required")
		return
	}
	ds, err := a.st.CreateDataset(r.Context(), a.proj(r), req.Name, req.Note)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_dataset", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (a *API) handleDatasetDetail(w http.ResponseWriter, r *http.Request) {
	projectID := a.proj(r)
	id := r.PathValue("datasetId")
	items, err := a.st.DatasetItems(r.Context(), projectID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	stats, err := a.st.CompareDataset(r.Context(), projectID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	var meta *store.Dataset
	if list, err := a.st.ListDatasets(r.Context(), projectID); err == nil {
		for i := range list {
			if list[i].ID == id {
				meta = &list[i]
				break
			}
		}
	}
	if meta == nil {
		writeErr(w, http.StatusNotFound, "not_found", "dataset not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dataset": meta, "items": items, "versions": stats,
	})
}

func (a *API) handleAddDatasetItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TraceIDs []string `json:"traceIds"`
		Note     string   `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.TraceIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_body", "traceIds is required")
		return
	}
	if len(req.TraceIDs) > 200 {
		writeErr(w, http.StatusBadRequest, "too_many_items", "at most 200 traces per request")
		return
	}
	added, err := a.st.AddDatasetItems(r.Context(), a.proj(r), r.PathValue("datasetId"), req.TraceIDs, req.Note)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "dataset_items_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "requested": len(req.TraceIDs)})
}

func (a *API) handleRemoveDatasetItem(w http.ResponseWriter, r *http.Request) {
	if err := a.st.RemoveDatasetItem(r.Context(), a.proj(r),
		r.PathValue("datasetId"), r.PathValue("traceId")); err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteDataset removes a dataset and answers 404 when it never existed.
func (a *API) handleDeleteDataset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("datasetId")
	existed, err := a.st.DeleteDataset(r.Context(), a.proj(r), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	if !existed {
		writeErr(w, http.StatusNotFound, "not_found", "dataset not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Prices

func (a *API) handleListPrices(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListPrices(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prices": list})
}

// handlePutPrice validates the three price fields before writing. Negative, NaN
// and infinite values are rejected here because the store skips them silently,
// which would turn a bad request into a misleading 200 with updated=0.
func (a *API) handlePutPrice(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.PathValue("model"))
	if model == "" {
		writeErr(w, http.StatusBadRequest, "invalid_model", "model is required")
		return
	}
	var req struct {
		InputPer1k     *float64 `json:"inputPer1k"`
		OutputPer1k    *float64 `json:"outputPer1k"`
		CacheReadPer1k *float64 `json:"cacheReadPer1k"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "json body required")
		return
	}
	if req.InputPer1k == nil || req.OutputPer1k == nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "inputPer1k and outputPer1k are required")
		return
	}
	for name, v := range map[string]*float64{
		"inputPer1k":     req.InputPer1k,
		"outputPer1k":    req.OutputPer1k,
		"cacheReadPer1k": req.CacheReadPer1k,
	} {
		if v == nil {
			continue
		}
		if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
			writeErr(w, http.StatusBadRequest, "invalid_price",
				fmt.Sprintf("%s must be a finite number >= 0", name))
			return
		}
	}
	row := store.PriceRow{
		Model: model, InputPer1k: *req.InputPer1k,
		OutputPer1k: *req.OutputPer1k, CacheReadPer1k: req.CacheReadPer1k,
	}
	n, err := a.st.UpsertPrices(r.Context(), []store.PriceRow{row})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	a.applyPrices(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

func (a *API) handleDeletePrice(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeletePrice(r.Context(), r.PathValue("model")); err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	a.applyPrices(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePriceSync pulls a LiteLLM-style public price table and merges it.
// Only per-token fields are read and converted to per-1k; anything else is ignored.
func (a *API) handlePriceSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	src := strings.TrimSpace(req.URL)
	if src == "" {
		src = a.pricesURL
	}
	if src == "" {
		writeErr(w, http.StatusBadRequest, "price_source_missing",
			"pass {\"url\":\"https://.../model_prices.json\"} or set TRAWL_PRICES_URL")
		return
	}
	if _, err := judge.ValidateURL(src, a.allowPriv); err != nil {
		writeErr(w, http.StatusBadRequest, "price_source_rejected", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "price_source_rejected", err.Error())
		return
	}
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "price_sync_failed", err.Error())
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		writeErr(w, http.StatusBadGateway, "price_sync_failed", "upstream returned "+res.Status)
		return
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "price_sync_failed", err.Error())
		return
	}
	rows, err := parsePricePayload(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "price_source_invalid", err.Error())
		return
	}
	n, err := a.st.UpsertPrices(ctx, rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	a.applyPrices(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"updated": n, "source": src})
}

// parsePricePayload accepts two shapes:
//  1. LiteLLM: {"model": {"input_cost_per_token": 0.00000015, "output_cost_per_token": 0.0000006}}
//  2. Simple: [{"model": "...", "inputPer1k": ..., "outputPer1k": ...}]
func parsePricePayload(body []byte) ([]store.PriceRow, error) {
	var asMap map[string]map[string]any
	if err := json.Unmarshal(body, &asMap); err == nil && len(asMap) > 0 {
		out := make([]store.PriceRow, 0, len(asMap))
		for model, fields := range asMap {
			in, ok1 := numberField(fields, "input_cost_per_token")
			outCost, ok2 := numberField(fields, "output_cost_per_token")
			if !ok1 || !ok2 {
				continue
			}
			row := store.PriceRow{Model: model, InputPer1k: in * 1000, OutputPer1k: outCost * 1000}
			if cache, ok := numberField(fields, "cache_read_input_token_cost"); ok {
				v := cache * 1000
				row.CacheReadPer1k = &v
			}
			out = append(out, row)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no model with input_cost_per_token / output_cost_per_token")
		}
		return out, nil
	}

	var asList []map[string]any
	if err := json.Unmarshal(body, &asList); err != nil {
		return nil, fmt.Errorf("payload is neither a model map nor a list")
	}
	out := make([]store.PriceRow, 0, len(asList))
	for _, item := range asList {
		model, _ := item["model"].(string)
		in, ok1 := numberField(item, "inputPer1k")
		outCost, ok2 := numberField(item, "outputPer1k")
		if model == "" || !ok1 || !ok2 {
			in, ok1 = numberField(item, "input_cost_per_token")
			outCost, ok2 = numberField(item, "output_cost_per_token")
			in *= 1000
			outCost *= 1000
			if model == "" || !ok1 || !ok2 {
				continue
			}
		}
		row := store.PriceRow{Model: model, InputPer1k: in, OutputPer1k: outCost}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable price entry in list")
	}
	return out, nil
}

func numberField(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// applyPrices reloads the price table into the write pipeline without a restart.
func (a *API) applyPrices(ctx context.Context) {
	prices, err := a.st.Prices(ctx)
	if err != nil {
		return
	}
	a.setPrices(prices)
}

// Backup and export

func (a *API) handleBackup(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(filepath.Dir(a.st.Path()), "backups")
	path, size, err := a.st.BackupTo(r.Context(), dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "backup_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":     filepath.Base(path),
		"size":     size,
		"download": "/api/backup/" + filepath.Base(path),
	})
}

// handleBackupDownload serves plain file names inside backups/ and blocks traversal.
func (a *API) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	if name == "." || name == "/" || strings.Contains(name, "..") {
		writeErr(w, http.StatusBadRequest, "invalid_name", "bad backup name")
		return
	}
	dir := filepath.Join(filepath.Dir(a.st.Path()), "backups")
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil || !strings.HasSuffix(name, ".db") {
		writeErr(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "open_failed", err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	_, _ = io.Copy(w, f)
}

// handleExportScores writes verdicts as CSV for downstream spreadsheet analysis.
func (a *API) handleExportScores(w http.ResponseWriter, r *http.Request) {
	rowsh, err := a.st.ExportScores(r.Context(), a.proj(r),
		parseInt64(r.URL.Query().Get("from"), 0),
		parseInt64(r.URL.Query().Get("to"), 0))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="trawl-scores.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "trace_id", "span_id", "name", "value", "string_value", "source", "author", "comment", "created_at"})
	for _, s := range rowsh {
		val := ""
		if s.Value != nil {
			val = strconv.FormatFloat(*s.Value, 'f', -1, 64)
		}
		_ = cw.Write([]string{
			s.ID, s.TraceID, s.SpanID, s.Name, val, s.StringValue,
			s.Source, s.AuthorName, s.Comment,
			time.UnixMilli(s.CreatedAt).Format(time.RFC3339),
		})
	}
	cw.Flush()
}

// Projects

func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListProjects(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list, "current": a.proj(r)})
}

func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "json body required")
		return
	}
	projectID, pk, sk, err := a.st.CreateProject(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_project", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projectId": projectID, "name": req.Name, "publicKey": pk, "secretKey": sk,
		"notice": "secret key 只显示这一次，请立刻保存",
	})
}
