package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trawlhq/trawl/internal/store"
	"github.com/trawlhq/trawl/internal/writer"
)

type adminEnv struct {
	srv       *httptest.Server
	st        *store.Store
	projectID string
	secret    string
}

func newAdminEnv(t *testing.T, opt ...func(*Options)) *adminEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	projectID, _, secret, err := st.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	wr := writer.New(st, 64)
	wr.Start(ctx)
	o := Options{ProjectID: projectID}
	for _, f := range opt {
		f(&o)
	}
	api := New(st, wr, o)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &adminEnv{srv: srv, st: st, projectID: projectID, secret: secret}
}

func (e *adminEnv) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	return res, out
}

func TestDatasetEndpoints(t *testing.T) {
	env := newAdminEnv(t)

	res, out := env.do(t, http.MethodPost, "/api/datasets", map[string]any{"name": "退款回归", "note": "周更"})
	if res.StatusCode != 200 {
		t.Fatalf("create dataset = %d %+v", res.StatusCode, out)
	}
	dsID, _ := out["id"].(string)
	if dsID == "" {
		t.Fatal("dataset id missing")
	}
	if res, _ := env.do(t, http.MethodPost, "/api/datasets", map[string]any{"name": " "}); res.StatusCode != 400 {
		t.Errorf("empty name = %d, want 400", res.StatusCode)
	}

	res, out = env.do(t, http.MethodPost, "/api/datasets/"+dsID+"/items",
		map[string]any{"traceIds": []string{"t_missing", "t_missing"}})
	if res.StatusCode != 200 {
		t.Fatalf("add items = %d %+v", res.StatusCode, out)
	}
	if out["added"].(float64) != 1 {
		t.Errorf("dedupe failed: %+v", out)
	}

	if res, _ := env.do(t, http.MethodGet, "/api/datasets/ds_bogus", nil); res.StatusCode != 404 {
		t.Errorf("unknown dataset detail = %d, want 404", res.StatusCode)
	}

	res, out = env.do(t, http.MethodGet, "/api/datasets/"+dsID, nil)
	if res.StatusCode != 200 {
		t.Fatalf("detail = %d", res.StatusCode)
	}
	if items, _ := out["items"].([]any); len(items) != 1 {
		t.Errorf("items = %d", len(items))
	}

	res, _ = env.do(t, http.MethodPost, "/api/datasets/ds_nope/items", map[string]any{"traceIds": []string{"x"}})
	if res.StatusCode != 400 {
		t.Errorf("unknown dataset = %d, want 400", res.StatusCode)
	}

	res, _ = env.do(t, http.MethodDelete, "/api/datasets/"+dsID, nil)
	if res.StatusCode != 200 {
		t.Error("delete dataset failed")
	}
	if res, _ := env.do(t, http.MethodDelete, "/api/datasets/"+dsID, nil); res.StatusCode != 404 {
		t.Errorf("double delete = %d, want 404", res.StatusCode)
	}
}

func TestPriceEndpointsAndSync(t *testing.T) {
	env := newAdminEnv(t, func(o *Options) { o.AllowPrivateFetch = true })

	res, out := env.do(t, http.MethodPut, "/api/prices/gpt-4o-mini",
		map[string]any{"inputPer1k": 0.00015, "outputPer1k": 0.0006})
	if res.StatusCode != 200 || out["updated"].(float64) != 1 {
		t.Fatalf("put price = %d %+v", res.StatusCode, out)
	}
	if res, _ := env.do(t, http.MethodPut, "/api/prices/x", map[string]any{"inputPer1k": 1}); res.StatusCode != 400 {
		t.Error("missing outputPer1k should be 400")
	}
	if res, _ := env.do(t, http.MethodPut, "/api/prices/neg",
		map[string]any{"inputPer1k": -1, "outputPer1k": 0.2}); res.StatusCode != 400 {
		t.Errorf("negative price = %d, want 400", res.StatusCode)
	}
	if prices, _ := env.st.ListPrices(context.Background()); pricesContain(prices, "neg") {
		t.Error("negative price must not be stored")
	}

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"claude-3-haiku":{"input_cost_per_token":0.00000025,"output_cost_per_token":0.00000125}}`))
	}))
	defer src.Close()

	res2, out2 := env.do(t, http.MethodPost, "/api/prices/sync", map[string]any{"url": src.URL})
	if res2.StatusCode != 200 {
		t.Fatalf("sync = %d %+v", res2.StatusCode, out2)
	}
	if out2["updated"].(float64) != 1 {
		t.Errorf("synced = %+v", out2)
	}
	prices, _ := env.st.ListPrices(context.Background())
	var got *store.PriceRow
	for i := range prices {
		if prices[i].Model == "claude-3-haiku" {
			got = &prices[i]
		}
	}
	if got == nil || got.InputPer1k != 0.00025 {
		t.Errorf("per-token → per-1k conversion wrong: %+v", got)
	}

	strict := newAdminEnv(t)
	if res, out := strict.do(t, http.MethodPost, "/api/prices/sync",
		map[string]any{"url": "http://127.0.0.1:9/prices.json"}); res.StatusCode != 400 {
		t.Errorf("private url = %d %+v, want 400", res.StatusCode, out)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer bad.Close()
	if res, _ := env.do(t, http.MethodPost, "/api/prices/sync",
		map[string]any{"url": bad.URL}); res.StatusCode != 400 {
		t.Error("invalid payload should be 400")
	}
}

func TestBackupAndExport(t *testing.T) {
	env := newAdminEnv(t)

	res, out := env.do(t, http.MethodPost, "/api/backup", nil)
	if res.StatusCode != 200 {
		t.Fatalf("backup = %d %+v", res.StatusCode, out)
	}
	name, _ := out["name"].(string)
	if name == "" || !strings.HasSuffix(name, ".db") {
		t.Fatalf("backup name = %q", name)
	}

	dl, err := http.Get(env.srv.URL + "/api/backup/" + name)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	dl.Body.Close()
	if dl.StatusCode != 200 {
		t.Fatalf("download = %d", dl.StatusCode)
	}
	if ct := dl.Header.Get("Content-Type"); !strings.Contains(ct, "sqlite") {
		t.Errorf("content-type = %q", ct)
	}

	evil, err := http.Get(env.srv.URL + "/api/backup/..%2F..%2Fapp.db")
	if err != nil {
		t.Fatalf("evil: %v", err)
	}
	evil.Body.Close()
	if evil.StatusCode != 400 && evil.StatusCode != 404 {
		t.Errorf("traversal = %d, want 400/404", evil.StatusCode)
	}

	res, _ = env.do(t, http.MethodPost, "/api/scores", map[string]any{
		"traceId": "t1", "name": "verdict", "value": 1, "comment": "ok",
	})
	if res.StatusCode != 200 {
		t.Fatalf("score = %d", res.StatusCode)
	}
	csvRes, err := http.Get(env.srv.URL + "/api/export/scores.csv")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer csvRes.Body.Close()
	if csvRes.StatusCode != 200 {
		t.Fatalf("export = %d", csvRes.StatusCode)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(csvRes.Body)
	if !strings.Contains(buf.String(), "verdict") {
		t.Errorf("csv missing verdict: %q", buf.String())
	}
}

// TestUnknownPaths pins two behaviors: unknown /api/* paths return JSON 404,
// and non-page paths never fall through to HTML.
func TestUnknownPaths(t *testing.T) {
	env := newAdminEnv(t)

	res, out := env.do(t, http.MethodGet, "/api/does-not-exist", nil)
	if res.StatusCode != 404 {
		t.Errorf("unknown api = %d, want 404", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("unknown api content-type = %q, want json", ct)
	}
	if code, _ := out["error"].(map[string]any)["code"].(string); code != "not_found" {
		t.Errorf("unknown api error code = %q", code)
	}

	for _, p := range []string{"/data/app.db", "/metrics", "/v1/nope"} {
		if res, _ := env.do(t, http.MethodGet, p, nil); res.StatusCode != 404 {
			t.Errorf("%s = %d, want 404", p, res.StatusCode)
		}
	}

	for _, p := range []string{"/", "/queue", "/queue/t1", "/prompts", "/datasets", "/settings", "/t/t1"} {
		res, _ := env.do(t, http.MethodGet, p, nil)
		if res.StatusCode != 200 {
			t.Errorf("%s = %d, want 200", p, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "html") {
			t.Errorf("%s content-type = %q, want html", p, ct)
		}
	}
}

func pricesContain(rows []store.PriceRow, model string) bool {
	for _, r := range rows {
		if r.Model == model {
			return true
		}
	}
	return false
}

func TestProjectScoping(t *testing.T) {
	env := newAdminEnv(t)

	res, out := env.do(t, http.MethodPost, "/api/projects", map[string]any{"name": "第二项目"})
	if res.StatusCode != 200 {
		t.Fatalf("create project = %d %+v", res.StatusCode, out)
	}
	newID, _ := out["projectId"].(string)
	newSecret, _ := out["secretKey"].(string)
	if newID == "" || newSecret == "" {
		t.Fatalf("project id/key missing: %+v", out)
	}

	res, out = env.do(t, http.MethodGet, "/api/traces?project="+newID, nil)
	if res.StatusCode != 200 {
		t.Fatalf("traces = %d", res.StatusCode)
	}
	if tps, _ := out["traces"].([]any); len(tps) != 0 {
		t.Errorf("new project should be empty, got %d traces", len(tps))
	}

	res, _ = env.do(t, http.MethodGet, "/api/traces?project=prj_bogus", nil)
	if res.StatusCode != 200 {
		t.Errorf("unknown project should fall back, got %d", res.StatusCode)
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	raw := fmt.Sprintf(`{"batch":[{"id":"e1","type":"trace-create","timestamp":%q,
	  "body":{"id":"trace_newproj","name":"only-here","timestamp":%q}}]}`, ts, ts)
	req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/api/public/ingestion", strings.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+newSecret)
	req.Header.Set("Content-Type", "application/json")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("ingest with new project key = %d %s", r2.StatusCode, b2)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, listed := env.do(t, http.MethodGet, "/api/traces?project="+newID+"&limit=10", nil)
		if tps, _ := listed["traces"].([]any); len(tps) == 1 || time.Now().After(deadline) {
			if len(tps) != 1 {
				t.Fatalf("new project traces = %d", len(tps))
			}
			break
		}
		time.Sleep(40 * time.Millisecond)
	}

	_, def := env.do(t, http.MethodGet, "/api/traces?limit=10", nil)
	for _, it := range def["traces"].([]any) {
		m, _ := it.(map[string]any)
		if m["traceId"] == "trace_newproj" {
			t.Fatal("cross-project leak")
		}
	}
}
