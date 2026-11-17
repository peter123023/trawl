# Trawl

**English** | [简体中文](README.zh-CN.md)

> Trace in. Verdict out.

Trawl is an **evaluation-first** workbench for LLM agents: observability is the floor, human and automatic evaluation is the main line.

One Go binary plus one SQLite file. `trawl` starts the server, and you need no Postgres, ClickHouse, Redis, or containers.

The frontend is React + Vite (`web/`); the build output is embedded into the binary with `go:embed`, so the runtime artifact is still a single file.

## Scope

Trawl is a focused workbench, not a general observability platform. It keeps exactly one path:

```
SDK/OTLP → ingest & normalize → single-writer batching → SQLite wide table → review-style evaluation → verdict
```

* **Single binary**: `go build` yields \~15MB, `go:embed` carries the UI, no shared-library dependency (modernc SQLite, pure Go).

* **Single-file data**: `data/app.db`, portable and backupable with `VACUUM INTO`.

* **Evaluation first**: score traces and write verdicts immediately, without learning a console first.

* **Review-desk UI**: evidence / source / verdict in three columns with paper-and-ink styling.

## Screens

Six screens, one question each:

| Screen          | Route          | Purpose                                                       |
| --------------- | -------------- | ------------------------------------------------------------- |
| Case ledger     | `/`            | Browse traces by time, search name / input / output           |
| Case review     | `/t/{traceId}` | Evidence tree + source + verdict, keys J/K/Space/X/S          |
| Eval queue      | `/queue`       | FIFO of unjudged traces; multi-select to stamp or batch-judge |
| Prompt versions | `/prompts`     | Calls, cost and pass rate by `name+version`                   |
| Regression sets | `/datasets`    | Freeze a sample set, compare pass rate across versions        |
| Settings        | `/settings`    | Projects and keys, model prices, backups, CSV export          |

Screenshots live in `docs/screenshots/` (`desk` / `case` / `queue` / `prompts` / `datasets` / `settings`).

## Quick start

```
# Go 1.22+, no cgo required
make build          # or: go build -o bin/trawl ./cmd/trawl
./bin/trawl         # listens on :8080, writes to ./data
```

The first start creates a project and keys, writes `data/keys.txt` (mode 0600) and prints them:

```
project: prj_xxx
public:  pk-trawl-xxxx
secret:  sk-trawl-xxxx
```

Environment variables:

| Variable                    | Default       | Meaning                                                                                                         |
| --------------------------- | ------------- | --------------------------------------------------------------------------------------------------------------- |
| `TRAWL_DATA_DIR`            | `./data`      | Database and key directory                                                                                      |
| `TRAWL_ADDR`                | `:8080`       | Listen address                                                                                                  |
| `TRAWL_JUDGE_BASE_URL`      | —             | OpenAI-compatible endpoint (`https://api.openai.com/v1`, local vLLM, etc). Empty disables model judging         |
| `TRAWL_JUDGE_API_KEY`       | —             | Endpoint key                                                                                                    |
| `TRAWL_JUDGE_MODEL`         | `gpt-4o-mini` | Judge model                                                                                                     |
| `TRAWL_JUDGE_ALLOW_PRIVATE` | —             | Set to `1` to allow `localhost` and private-network endpoints; denied by default to prevent SSRF |
| `TRAWL_JUDGE_DAILY_CAP`     | `500`         | Daily model-judgement cap; over-limit returns 429. `0` means unlimited                                          |
| `TRAWL_PRICES_URL`          | —             | Price source (LiteLLM `model_prices.json` or a simplified list). The settings page uses it by default           |
| `TRAWL_ALLOW_PRIVATE_FETCH` | —             | Set to `1` to allow fetching prices from private addresses (separate switch from judge)                         |

Open [http://localhost:8080](http://localhost:8080) to reach the **case ledger**.

## Send a trace

Any client that emits the JSON tracing events below only needs a new baseUrl and key:

```
curl -X POST localhost:8080/api/public/ingestion \
  -H "Authorization: Bearer $TRAWL_SECRET" \
  -H "Content-Type: application/json" \
  --data @testdata/sample.json
```

Or OTLP/HTTP with GenAI semantic conventions:

```
curl -X POST localhost:8080/v1/traces \
  -H "Authorization: Bearer $TRAWL_SECRET" \
  -H "Content-Type: application/json" \
  --data @testdata/otlp.json
```

Supported event types: `trace-create`, `span-create/update`, `generation-create/update`, `event-create`, `score-create`, `sdk-log` (ignored).

Later events sharing the same `(project_id, trace_id, span_id)` merge field by field according to `event_ts`: newer values win, older values are never overwritten, empty values never backfill.

Token usage accepts both `usage` (the primary field in official SDKs) and `usageDetails` (the newer spelling); `usageDetails` wins and `usage` fills gaps. When neither carries `total`, it is derived as `input + output`, and cost is computed from `model_prices`.

## API

| Method     | Path                                                                                    | Purpose                                                                                                                |
| ---------- | --------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| POST       | `/api/public/ingestion`                                                                 | Tracing SDK JSON ingestion (Bearer / Basic / x-api-key)                                                                |
| POST       | `/v1/traces`                                                                            | OTLP/HTTP JSON                                                                                                         |
| POST       | `/api/public/scores`                                                                    | Write scores from outside                                                                                              |
| GET        | `/api/traces`                                                                           | Ledger query: `from` `to` `name` `q` `userId` `sessionId` `level` `limit` `cursor`; `q` searches name / input / output |
| GET        | `/api/traces/{traceId}`                                                                 | Trace detail: `{spans, scores}`                                                                                        |
| GET        | `/api/queue`                                                                            | Eval queue: `{items, stats:{pending, judgedToday, judgedTotal}}`                                                       |
| GET        | `/api/prompts`                                                                          | Prompt version ledger: calls / tokens / cost / pass rate by `name + version`                                           |
| POST       | `/api/scores`                                                                           | Human verdict (used by the UI)                                                                                         |
| POST       | `/api/scores/batch`                                                                     | Batch verdicts: `{scores:[...]}`, 1..500, single transaction                                                           |
| POST       | `/api/judge`                                                                            | Model judging: `{traceId, spanId?}`, writes `llm_judge`; 400 when unconfigured, 429 over budget                        |
| POST       | `/api/judge/batch`                                                                      | Batch model judging: `{traceIds:[...]}` or `{items:[{traceId,spanId}]}`, 1..50, concurrency 3, per-item soft failure   |
| GET        | `/api/scores?traceId=`                                                                  | List scores                                                                                                            |
| GET/POST   | `/api/datasets`                                                                         | List / create regression sets (`{name, note}`)                                                                         |
| GET/DELETE | `/api/datasets/{id}`                                                                    | Detail (with `items` and per-version `versions`) / delete                                                              |
| POST       | `/api/datasets/{id}/items`                                                              | Add traces: `{traceIds:[...]}`, duplicates deduplicated                                                                |
| DELETE     | `/api/datasets/{id}/items/{traceId}`                                                    | Remove a trace from the set                                                                                            |
| GET        | `/api/prices`                                                                           | Price table (`inputPer1k` / `outputPer1k`)                                                                             |
| PUT/DELETE | `/api/prices/{model}`                                                                   | Update / delete one model price, hot-applied to ingest conversion                                                      |
| POST       | `/api/prices/sync`                                                                      | Pull a price source from `{url}` or `TRAWL_PRICES_URL`; LiteLLM per-token shape supported                              |
| POST       | `/api/backup`                                                                           | `VACUUM INTO` a snapshot at `<dataDir>/backups/*.db`, returns a download URL                                           |
| GET        | `/api/backup/{name}`                                                                    | Download a backup (`filepath.Base` + `.db` only)                                                                       |
| GET        | `/api/export/scores.csv`                                                                | Export verdicts as CSV                                                                                                 |
| GET/POST   | `/api/projects`                                                                         | List / create projects (the new secret is returned once)                                                               |
| GET        | `/api/status`, `/healthz`                                                               | Runtime status                                                                                                         |
| GET        | `/`, `/t/{traceId}`, `/queue`, `/queue/{traceId}`, `/prompts`, `/datasets`, `/settings` | Embedded review UI                                                                                                     |

Read endpoints select a project with `X-Trawl-Project: <projectId>` (or `?project=`); a missing or unknown id falls back to the bootstrap project. On the ingest side the project is determined by the key, so the two never cross.

Auth: `Authorization: Bearer <secret>`, `Basic <public>:<secret>`, `x-api-key: <secret>`. Keys are stored as SHA-256 only and cannot be reversed.

### Three ways to judge

* **Single review**: score plus reason on the detail page, written as `verdict`.

* **Batch stamp**: select several traces in the queue, apply one dimension and reason, written in one transaction (`POST /api/scores/batch`).

* **Model judging**: the "model judge" button on the detail page or "judge all" in the queue sends input/output to an OpenAI-compatible endpoint, takes a 0-3 score and a reason, and writes `llm_judge` (source=`LLM`, original response kept in `raw_response`). Model scores never replace humans: the queue only accepts `verdict`, model output is evidence. The daily cap is enforced by `TRAWL_JUDGE_DAILY_CAP`.

Regression sets freeze "the same batch of questions" into `datasets` + `dataset_items`, then aggregate item count, judged count, pass rate and average verdict by `prompt_name + prompt_version`, answering "how much better is v2 than v1". Items come from the ledger; verdicts still live in `scores`.

Search splits by length: `q` of 3+ characters goes through an FTS5 `trigram` phrase query (`span_fts` is an external-content table kept in sync by triggers); 1-2 characters fall back to `LIKE`. The reason: `unicode61` treats a whole Chinese sentence as one token, so two-character words such as `退款` are not tokens under trigram.

Prompt pass rate comes from `spans.prompt_name` / `prompt_version`. JSON ingestion reads `promptName` / `promptVersion`; OTLP callers can set the equivalent SDK attributes. A version passes when every generation-linked trace carries `verdict >= 1`.

## Data model

`spans` is one wide table (trace / span / generation / event share rows). `scores` holds verdicts, `model_prices` drives cost conversion, `projects` / `api_keys` manage tenancy, `ingest_batches` is reserved for idempotent batches.

Migration `0003` adds `prompt_name` / `prompt_version` to `spans` for version-level aggregation. Migration `0004` adds `raw_response` to `scores` for audit. Migration `0005` adds `span_fts` (FTS5 trigram external content plus three triggers), `datasets` and `dataset_items` (unique index `(dataset_id, trace_id)` to deduplicate). Migration `0006` changes the `spans` primary key from `(project_id, span_id)` to `(project_id, trace_id, span_id)`: OTLP only requires span ids to be unique within their own trace, so the old key merged a same-named span into another trace, an invisible data mix-up.

* Time is `INTEGER` milliseconds (UTC).

* `STRICT` tables; JSON columns are TEXT and aggregated with `json_extract`.

* One write connection (WAL), four read connections (`mode=ro`).

## Development

```
make test     # go test ./...
make fmt      # gofmt
make vet
make tidy
```

If the default Go cache directory is not writable (common in restricted environments), use the in-project cache:

```
GOPATH=$PWD/.cache/gopath GOCACHE=$PWD/.cache/go-build \
GOMODCACHE=$PWD/.cache/gopath/pkg/mod go build -o bin/trawl ./cmd/trawl
```

Layout:

```
cmd/trawl            entrypoint, config, graceful shutdown
internal/store       two pools, migrations, upsert merge, queries, queue, prompt aggregation; extras.go covers datasets/prices/backups/projects
internal/writer      single-writer batching (150ms / 500 rows)
internal/ingest      JSON ingestion + OTLP parsing, cost conversion
internal/judge       OpenAI-compatible judge client (private networks denied by default, strict JSON parsing)
internal/httpapi     routing, auth, JSON; admin.go covers datasets/prices/backups/export/projects
internal/web         go:embed dist and static assets
internal/web/dist    Vite build output (committed so `go build` works in one command)
web/                 React + Vite + TS source (src/pages: ledger / review / queue / prompt versions / datasets / settings)
web/legacy           the M1 single-file UI, kept only as a fallback reference
testdata             sample payloads
```

Frontend workflow:

```
cd web && npm install
npm run dev        # :5173, /api and /v1 proxied to :8080
make web-build     # builds into internal/web/dist, then go build embeds it
```

## Roadmap

* **M1 (done)**: ingest → store → ledger → detail → verdict, single project, single-file SQLite.

* **M2 (done)**: React/Vite four screens (ledger / review / queue / prompt versions), FIFO eval queue, batch verdicts, prompt version aggregation.

* **M3 (done)**: LLM-as-judge single and batch, daily budget guardrail, original response persisted, regression sets with version comparison, price table and price sync, backups, CSV export, multi-project with switching.

* **M4 (partial)**: FTS5 trigram search (3+ characters), backups and export shipped; fine-grained RBAC and a Postgres mode remain. Postgres will not be implemented: a single SQLite file is a product premise.

## Scale and performance (measured)

Single-file SQLite, single macOS machine, HTTP end to end (three samples):

| Dataset                 | List      | Queue | FTS search (3+ chars) | LIKE search (2 chars) | Prompt versions |
| ----------------------- | --------- | ----- | --------------------- | --------------------- | --------------- |
| 20k spans / 10k traces  | 40ms      | 72ms  | 18ms                  | 18ms                  | 16ms            |
| 100k spans / 50k traces | 210-330ms | 410ms | 65ms                  | 73ms                  | 34ms            |

Ingest: 20k spans in 0.7s, 80k more in 3.1s (about 26k spans/s including FTS index maintenance). Database size: 9.2MB for 20k spans, 45MB for 100k.

One hard constraint: the trace aggregation SQL must never contain a correlated subquery that re-reads a row by trace. Under `GROUP BY`, SQLite cannot push the `trace_id` correlation down, so each group degenerates into a full table scan: with 10k traces a single aggregate took 25.5s, the list 39s and the queue 78s. A pure aggregate expression brings the same SQL down to 0.027s.

## Known limitations

* Multi-project works, but keys and permissions are not tiered: anyone with UI access can switch projects.

* Regression sets only compare pass rates by version; there is no automatic replay (no LLM re-run, only aggregation of existing verdicts).

* Price sync understands only LiteLLM `model_prices.json` and a simplified list; other billing formats need a one-time conversion.

* When the write buffer is full the API returns `429`; no data is lost provided the client retries.

* UI read endpoints are unauthenticated, suitable for `localhost` or an intranet only. Put a reverse proxy in front before exposing it.

* Cost is computed only when `model_prices` has a matching entry; unknown models are never guessed.

* Model judging scores a span's input/output and does not replay multi-turn conversations; the judge prompt is fixed, custom rubrics are not supported yet.

* A trace can be judged repeatedly and `scores` keeps every run; the detail page shows only the latest, and history is bounded by `TRAWL_JUDGE_DAILY_CAP`.

## License

MIT. See [LICENSE](LICENSE).