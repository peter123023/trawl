package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trawlhq/trawl/internal/ingest"
	"github.com/trawlhq/trawl/internal/judge"
	"github.com/trawlhq/trawl/internal/store"
	"github.com/trawlhq/trawl/internal/web"
	"github.com/trawlhq/trawl/internal/writer"
)

type API struct {
	st        *store.Store
	wr        *writer.Writer
	judge     *judge.Client
	judgeCap  int // daily model judgement cap; <=0 means unlimited
	projectID string
	pricesURL string
	allowPriv bool // allow server-side fetches to private addresses (price sync, local vLLM)
	startedAt time.Time

	mu     sync.RWMutex
	prices map[string]store.Price
}

// Options carries runtime configuration as named fields instead of positional arguments.
type Options struct {
	ProjectID string
	Prices    map[string]store.Price
	Judge     *judge.Client
	JudgeCap  int
	PricesURL string
	// AllowPrivateFetch lets price sync and judge reach localhost and private networks.
	AllowPrivateFetch bool
}

func New(st *store.Store, wr *writer.Writer, opt Options) *API {
	a := &API{
		st: st, wr: wr, judge: opt.Judge, judgeCap: opt.JudgeCap,
		projectID: opt.ProjectID, pricesURL: opt.PricesURL, allowPriv: opt.AllowPrivateFetch,
		prices: opt.Prices, startedAt: time.Now(),
	}
	if a.prices == nil {
		a.prices = map[string]store.Price{}
	}
	return a
}

// priceMap returns a price snapshot; prices can be hot-edited, so reads are locked.
func (a *API) priceMap() map[string]store.Price {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.prices
}

func (a *API) setPrices(p map[string]store.Price) {
	if p == nil {
		p = map[string]store.Price{}
	}
	a.mu.Lock()
	a.prices = p
	a.mu.Unlock()
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/public/ingestion", a.handleIngestion)
	mux.HandleFunc("POST /v1/traces", a.handleOTLP)
	mux.HandleFunc("POST /api/public/scores", a.handleIngestScore)
	mux.HandleFunc("GET /api/traces", a.handleListTraces)
	mux.HandleFunc("GET /api/traces/{traceId}", a.handleGetTrace)
	mux.HandleFunc("GET /api/queue", a.handleQueue)
	mux.HandleFunc("GET /api/prompts", a.handleListPrompts)
	mux.HandleFunc("POST /api/scores", a.handleAddScore)
	mux.HandleFunc("POST /api/scores/batch", a.handleAddScoresBatch)
	mux.HandleFunc("POST /api/judge", a.handleJudge)
	mux.HandleFunc("POST /api/judge/batch", a.handleJudgeBatch)
	mux.HandleFunc("GET /api/scores", a.handleListScores)
	mux.HandleFunc("GET /api/datasets", a.handleListDatasets)
	mux.HandleFunc("POST /api/datasets", a.handleCreateDataset)
	mux.HandleFunc("GET /api/datasets/{datasetId}", a.handleDatasetDetail)
	mux.HandleFunc("DELETE /api/datasets/{datasetId}", a.handleDeleteDataset)
	mux.HandleFunc("POST /api/datasets/{datasetId}/items", a.handleAddDatasetItems)
	mux.HandleFunc("DELETE /api/datasets/{datasetId}/items/{traceId}", a.handleRemoveDatasetItem)
	mux.HandleFunc("GET /api/prices", a.handleListPrices)
	mux.HandleFunc("PUT /api/prices/{model}", a.handlePutPrice)
	mux.HandleFunc("DELETE /api/prices/{model}", a.handleDeletePrice)
	mux.HandleFunc("POST /api/prices/sync", a.handlePriceSync)
	mux.HandleFunc("POST /api/backup", a.handleBackup)
	mux.HandleFunc("GET /api/backup/{name}", a.handleBackupDownload)
	mux.HandleFunc("GET /api/export/scores.csv", a.handleExportScores)
	mux.HandleFunc("GET /api/projects", a.handleListProjects)
	mux.HandleFunc("POST /api/projects", a.handleCreateProject)
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /healthz", a.handleStatus)
	mux.HandleFunc("GET /t/{traceId}", a.handleIndex)
	mux.HandleFunc("GET /queue", a.handleIndex)
	mux.HandleFunc("GET /queue/{traceId}", a.handleIndex)
	mux.Handle("GET /assets/", web.Assets())
	mux.HandleFunc("GET /", a.handleIndex)
	return withRecover(withCORS(mux))
}

// ---------- middleware ----------

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "path", r.URL.Path, "recover", rec)
				writeErr(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) projectFromRequest(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	switch {
	case strings.HasPrefix(auth, "Bearer "):
		return a.st.ProjectBySecret(r.Context(), strings.TrimSpace(auth[len("Bearer "):]))
	case strings.HasPrefix(auth, "Basic "):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[len("Basic "):]))
		if err != nil {
			return "", false
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 {
			return "", false
		}
		return a.st.ProjectBySecret(r.Context(), parts[1])
	default:
		if k := r.Header.Get("x-api-key"); k != "" {
			return a.st.ProjectBySecret(r.Context(), k)
		}
	}
	return "", false
}

// ---------- ingestion ----------

type ingestResult struct {
	Successes []map[string]any `json:"successes"`
	Errors    []map[string]any `json:"errors"`
}

func (a *API) handleIngestion(w http.ResponseWriter, r *http.Request) {
	projectID, ok := a.projectFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	spans, scores, err := ingest.ParseIngestion(body, projectID, a.priceMap())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if !a.wr.Submit(spans) {
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusTooManyRequests, "queue_full", "writer backlog, retry shortly")
		return
	}
	for i := range scores {
		if err := a.st.AddScore(r.Context(), &scores[i]); err != nil {
			slog.Warn("score write failed", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, ingestResult{Successes: okIDs(spans), Errors: []map[string]any{}})
}

func (a *API) handleOTLP(w http.ResponseWriter, r *http.Request) {
	projectID, ok := a.projectFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	spans, err := ingest.ParseOTLP(body, projectID, a.priceMap())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if !a.wr.Submit(spans) {
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusTooManyRequests, "queue_full", "writer backlog, retry shortly")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *API) handleIngestScore(w http.ResponseWriter, r *http.Request) {
	projectID, ok := a.projectFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}
	var payload struct {
		TraceID     string   `json:"traceId"`
		SpanID      string   `json:"observationId"`
		Name        string   `json:"name"`
		Value       *float64 `json:"value"`
		StringValue string   `json:"stringValue"`
		Comment     string   `json:"comment"`
		DataType    string   `json:"dataType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if payload.Name == "" || payload.TraceID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_body", "name and traceId are required")
		return
	}
	sc := store.Score{
		ProjectID: projectID, TraceID: payload.TraceID, SpanID: payload.SpanID,
		Name: payload.Name, DataType: defaultStr(payload.DataType, "NUMERIC"),
		Value: payload.Value, StringValue: payload.StringValue,
		Comment: payload.Comment, Source: "API",
	}
	if err := a.st.AddScore(r.Context(), &sc); err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sc.ID})
}

// ---------- read ----------

func (a *API) handleListTraces(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	f := store.TraceFilter{
		ProjectID:   a.proj(r),
		From:        parseInt64(qs.Get("from"), 0),
		To:          parseInt64(qs.Get("to"), 0),
		Name:        qs.Get("name"),
		Q:           strings.TrimSpace(qs.Get("q")),
		UserID:      qs.Get("userId"),
		SessionID:   qs.Get("sessionId"),
		Level:       qs.Get("level"),
		Environment: qs.Get("environment"),
		Limit:       int(parseInt64(qs.Get("limit"), 50)),
	}
	now := time.Now().UnixMilli()
	if f.To == 0 {
		f.To = now + 60_000
	}
	if f.From == 0 {
		f.From = now - 7*24*3600_000
	}
	if c := qs.Get("cursor"); c != "" {
		if raw, err := base64.StdEncoding.DecodeString(c); err == nil {
			parts := strings.SplitN(string(raw), "|", 2)
			if len(parts) == 2 {
				f.CursorTS, _ = strconv.ParseInt(parts[0], 10, 64)
				f.CursorID = parts[1]
			}
		}
	}
	traces, err := a.st.ListTraces(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	next := ""
	if len(traces) == f.Limit && len(traces) > 0 {
		last := traces[len(traces)-1]
		next = base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(last.StartTime, 10) + "|" + last.TraceID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces, "nextCursor": next})
}

func (a *API) handleQueue(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	from := parseInt64(qs.Get("from"), 0)
	to := parseInt64(qs.Get("to"), 0)
	limit := int(parseInt64(qs.Get("limit"), 100))
	items, stats, err := a.st.ListQueue(r.Context(), a.proj(r), from, to, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "stats": stats})
}

func (a *API) handleListPrompts(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	from := parseInt64(qs.Get("from"), 0)
	to := parseInt64(qs.Get("to"), 0)
	prompts, err := a.st.ListPrompts(r.Context(), a.proj(r), from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": prompts})
}

func (a *API) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	traceID := r.PathValue("traceId")
	spans, err := a.st.GetTrace(r.Context(), a.proj(r), traceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if len(spans) == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "trace not found")
		return
	}
	scores, err := a.st.ListScores(r.Context(), a.proj(r), traceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spans": spans, "scores": scores})
}

type scorePayload struct {
	TraceID     string   `json:"traceId"`
	SpanID      string   `json:"spanId"`
	Observation string   `json:"observationId"`
	Name        string   `json:"name"`
	Value       *float64 `json:"value"`
	StringValue string   `json:"stringValue"`
	Comment     string   `json:"comment"`
	DataType    string   `json:"dataType"`
	AuthorName  string   `json:"authorName"`
	Source      string   `json:"source"`
}

func (p scorePayload) toScore(projectID, defaultSource string) (store.Score, error) {
	if p.TraceID == "" || p.Name == "" {
		return store.Score{}, errors.New("traceId and name are required")
	}
	spanID := p.SpanID
	if spanID == "" {
		spanID = p.Observation
	}
	dataType := defaultStr(p.DataType, "NUMERIC")
	source := defaultStr(p.Source, defaultSource)
	return store.Score{
		ProjectID:   projectID,
		TraceID:     p.TraceID,
		SpanID:      spanID,
		Name:        p.Name,
		DataType:    dataType,
		Value:       p.Value,
		StringValue: p.StringValue,
		Comment:     p.Comment,
		Source:      source,
		AuthorName:  p.AuthorName,
	}, nil
}

// handleAddScoresBatch lets the desk stamp a whole queue page in one write.
func (a *API) handleAddScoresBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scores []scorePayload `json:"scores"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if len(req.Scores) == 0 || len(req.Scores) > 500 {
		writeErr(w, http.StatusBadRequest, "invalid_body", "scores must contain 1..500 items")
		return
	}
	rows := make([]*store.Score, 0, len(req.Scores))
	for i, p := range req.Scores {
		sc, err := p.toScore(a.proj(r), "ANNOTATION")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_score", fmt.Sprintf("item %d: %s", i, err))
			return
		}
		rows = append(rows, &sc)
	}
	if err := a.st.AddScores(r.Context(), rows); err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	ids := make([]string, 0, len(rows))
	for _, sc := range rows {
		ids = append(ids, sc.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(ids), "ids": ids})
}

var errNothingJudge = errors.New("nothing_to_judge")

const judgeBatchMax = 50

// judgeRemaining returns how many model judgements are left today; -1 means no cap.
func (a *API) judgeRemaining(ctx context.Context, projectID string) (int, error) {
	if a.judgeCap <= 0 {
		return -1, nil
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	n, err := a.st.CountLLMJudgementsSince(ctx, projectID, start)
	if err != nil {
		return 0, err
	}
	return a.judgeCap - n, nil
}

// judgeTrace judges one span of a trace with the model and stores the score.
func (a *API) judgeTrace(ctx context.Context, projectID, traceID, spanID string) (*store.Score, error) {
	spans, err := a.st.GetTrace(ctx, projectID, traceID)
	if err != nil {
		return nil, err
	}
	var target *store.Span
	if spanID != "" {
		for i := range spans {
			if spans[i].SpanID == spanID {
				target = &spans[i]
				break
			}
		}
	}
	if target == nil {
		for i := range spans {
			if spans[i].Type == "generation" && strings.TrimSpace(spans[i].Output) != "" {
				target = &spans[i]
				break
			}
		}
	}
	if target == nil {
		for i := range spans {
			if strings.TrimSpace(spans[i].Output) != "" {
				target = &spans[i]
				break
			}
		}
	}
	if target == nil {
		return nil, errNothingJudge
	}

	res, err := a.judge.Score(ctx, target.Input, target.Output)
	if err != nil {
		return nil, err
	}
	score := res.Score
	sc := &store.Score{
		ProjectID:   projectID,
		TraceID:     traceID,
		SpanID:      target.SpanID,
		Name:        "llm_judge",
		DataType:    "NUMERIC",
		Value:       &score,
		Comment:     res.Reason,
		Source:      "LLM",
		AuthorName:  "llm:" + a.judge.Model(),
		RawResponse: res.Raw,
	}
	if err := a.st.AddScore(ctx, sc); err != nil {
		return nil, err
	}
	return sc, nil
}

func (a *API) handleJudge(w http.ResponseWriter, r *http.Request) {
	if a.judge == nil {
		writeErr(w, http.StatusBadRequest, "judge_not_configured",
			"set TRAWL_JUDGE_BASE_URL / TRAWL_JUDGE_API_KEY (and optionally TRAWL_JUDGE_MODEL) to enable LLM judging")
		return
	}
	var req struct {
		TraceID string `json:"traceId"`
		SpanID  string `json:"spanId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TraceID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_body", "traceId is required")
		return
	}
	projectID := a.proj(r)
	remaining, err := a.judgeRemaining(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if remaining == 0 {
		writeErr(w, http.StatusTooManyRequests, "judge_budget_exceeded",
			fmt.Sprintf("daily LLM judge budget reached (%d); raise TRAWL_JUDGE_DAILY_CAP or wait for tomorrow", a.judgeCap))
		return
	}
	sc, err := a.judgeTrace(r.Context(), a.proj(r), req.TraceID, req.SpanID)
	switch {
	case errors.Is(err, errNothingJudge):
		writeErr(w, http.StatusBadRequest, "nothing_to_judge", "no span with output in this trace")
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "judge_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": sc.ID, "score": *sc.Value, "reason": sc.Comment, "model": a.judge.Model(),
	})
}

// handleJudgeBatch judges traces with concurrency 3, per-item soft failure and a hard daily cap.
// One failure never aborts the rest; each item carries its own error.
func (a *API) handleJudgeBatch(w http.ResponseWriter, r *http.Request) {
	if a.judge == nil {
		writeErr(w, http.StatusBadRequest, "judge_not_configured",
			"set TRAWL_JUDGE_BASE_URL / TRAWL_JUDGE_API_KEY (and optionally TRAWL_JUDGE_MODEL) to enable LLM judging")
		return
	}
	type item struct {
		TraceID string `json:"traceId"`
		SpanID  string `json:"spanId"`
	}
	var req struct {
		TraceIDs []string `json:"traceIds"`
		Items    []item   `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "json body required")
		return
	}
	items := make([]item, 0, judgeBatchMax)
	for _, id := range req.TraceIDs {
		if strings.TrimSpace(id) != "" {
			items = append(items, item{TraceID: id})
		}
	}
	for _, it := range req.Items {
		if strings.TrimSpace(it.TraceID) != "" {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_body", "traceIds or items required")
		return
	}
	if len(items) > judgeBatchMax {
		writeErr(w, http.StatusBadRequest, "too_many_items",
			fmt.Sprintf("at most %d traces per batch", judgeBatchMax))
		return
	}

	projectID := a.proj(r)
	remaining, err := a.judgeRemaining(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if remaining == 0 {
		writeErr(w, http.StatusTooManyRequests, "judge_budget_exceeded",
			fmt.Sprintf("daily LLM judge budget reached (%d); raise TRAWL_JUDGE_DAILY_CAP or wait for tomorrow", a.judgeCap))
		return
	}
	truncated := 0
	if remaining > 0 && len(items) > remaining {
		truncated = len(items) - remaining
		items = items[:remaining]
	}

	results := make([]map[string]any, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it item) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sc, err := a.judgeTrace(r.Context(), projectID, it.TraceID, it.SpanID)
			if err != nil {
				results[i] = map[string]any{"traceId": it.TraceID, "error": err.Error()}
				return
			}
			results[i] = map[string]any{
				"traceId": it.TraceID, "id": sc.ID, "score": *sc.Value, "reason": sc.Comment,
			}
		}(i, it)
	}
	wg.Wait()

	ok := 0
	for _, res := range results {
		if _, failed := res["error"]; !failed {
			ok++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":   results,
		"ok":        ok,
		"failed":    len(results) - ok,
		"truncated": truncated,
		"model":     a.judge.Model(),
	})
}

func (a *API) handleAddScore(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TraceID     string   `json:"traceId"`
		SpanID      string   `json:"spanId"`
		Name        string   `json:"name"`
		Value       *float64 `json:"value"`
		StringValue string   `json:"stringValue"`
		Comment     string   `json:"comment"`
		DataType    string   `json:"dataType"`
		AuthorName  string   `json:"authorName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if payload.Name == "" || payload.TraceID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_body", "name and traceId are required")
		return
	}
	sc := store.Score{
		ProjectID: a.proj(r), TraceID: payload.TraceID, SpanID: payload.SpanID,
		Name: payload.Name, DataType: defaultStr(payload.DataType, "NUMERIC"),
		Value: payload.Value, StringValue: payload.StringValue,
		Comment: payload.Comment, Source: "ANNOTATION", AuthorName: payload.AuthorName,
	}
	if err := a.st.AddScore(r.Context(), &sc); err != nil {
		writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sc.ID})
}

func (a *API) handleListScores(w http.ResponseWriter, r *http.Request) {
	traceID := r.URL.Query().Get("traceId")
	if traceID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_query", "traceId is required")
		return
	}
	scores, err := a.st.ListScores(r.Context(), a.proj(r), traceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scores": scores})
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"uptimeSeconds": int(time.Since(a.startedAt).Seconds()),
		"spansWritten":  a.wr.Written(),
		"spansDropped":  a.wr.Dropped(),
		"dbPath":        a.st.Path(),
	})
}

// pageRoutes lists the paths the SPA owns. GET / catches every unmatched request,
// so this must stay a whitelist; otherwise /api/x would answer 200 HTML.
// pageExact matches whole paths, pagePrefix covers subtree routes.
var (
	pageExact  = []string{"/", "/queue", "/prompts", "/datasets", "/settings"}
	pagePrefix = []string{"/queue/", "/t/"}
)

func (a *API) handleIndex(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	matched := false
	for _, e := range pageExact {
		if p == e {
			matched = true
			break
		}
	}
	if !matched {
		for _, pre := range pagePrefix {
			if len(pre) > 1 && strings.HasPrefix(p, pre) {
				matched = true
				break
			}
		}
	}
	if !matched {
		writeErr(w, http.StatusNotFound, "not_found", "no such page or endpoint: "+p)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(web.Index())
}

// ---------- helpers ----------

func okIDs(spans []store.Span) []map[string]any {
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		out = append(out, map[string]any{"id": s.SpanID, "status": 201})
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"id": "none", "status": 201})
	}
	return out
}

func parseInt64(s string, fallback int64) int64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.Debug("write json failed", "error", err)
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
