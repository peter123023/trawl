# Trawl

[English](README.md) | **简体中文**

> Trace in. Verdict out. 把每条 trace 变成一条判词。

Trawl 是一个**评测优先（evaluation-first）**的 LLM Agent 工作台：可观测是底座，人工与自动评测是主线。

Go 单二进制 + SQLite 单文件，`trawl` 一条命令起服务，不需要 Postgres、ClickHouse、Redis 或任何容器。

前端为 React + Vite（`web/`），构建产物由 `go:embed` 嵌进二进制，运行时依旧只有一个文件。

## 定位与边界

Trawl 是一个聚焦的工作台，不做通用可观测平台。它刻意只保留一条链路：

```
SDK/OTLP → 摄取归一化 → 单写者批处理 → SQLite 宽表 → 阅卷式评测 → 判词
```

- **单二进制**：`go build` 产出约 15MB，`go:embed` 带 UI，零共享库依赖（modernc SQLite，纯 Go）。
- **单文件数据**：`data/app.db`，可拷走、可 `VACUUM INTO` 备份。
- **评测为主**：落地即可打分、写判词、过队列，不必先学一套控制台。
- **阅卷台界面**：三栏「证据 / 原文 / 判词」，纸墨印视觉。

## 界面

六屏，一屏一个问题：

| 屏 | 路由 | 干什么 |
|---|---|---|
| 案件台账 | `/` | 按时间看链路，搜索名称 / 输入 / 输出 |
| 阅卷 | `/t/{traceId}` | 证据树 + 原文 + 判词，键盘 J/K/Space/X/S |
| 评测队列 | `/queue` | FIFO 待判条目，勾选后可批量盖章或整批机判 |
| Prompt 版本 | `/prompts` | 按 `name+version` 看调用量、成本、通过率 |
| 回归集 | `/datasets` | 固化样本，按版本比通过率 |
| 设置 | `/settings` | 项目与密钥、模型价格、备份、CSV 导出 |

截图见 `docs/screenshots/`（`desk` / `case` / `queue` / `prompts` / `datasets` / `settings`）。

## 快速开始

```
# Go 1.22+，无需 cgo
make build          # 或: go build -o bin/trawl ./cmd/trawl
./bin/trawl         # 默认监听 :8080，数据写入 ./data
```

首次启动会生成项目与密钥，并写入 `data/keys.txt`（0600），同时打印到终端：

```
project: prj_xxx
public:  pk-trawl-xxxx
secret:  sk-trawl-xxxx
```

环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `TRAWL_DATA_DIR` | `./data` | 数据库与密钥目录 |
| `TRAWL_ADDR` | `:8080` | 监听地址 |
| `TRAWL_JUDGE_BASE_URL` | — | OpenAI 兼容端点（`https://api.openai.com/v1`、本地 vLLM 皆可），留空则关闭模型初判 |
| `TRAWL_JUDGE_API_KEY` | — | 端点密钥 |
| `TRAWL_JUDGE_MODEL` | `gpt-4o-mini` | 判分模型 |
| `TRAWL_JUDGE_ALLOW_PRIVATE` | — | 置 `1` 才允许 localhost / 私网端点，默认拒绝以防 SSRF |
| `TRAWL_JUDGE_DAILY_CAP` | `500` | 每日模型判词上限，超限返回 429；置 `0` 表示不限 |
| `TRAWL_PRICES_URL` | — | 价格同步源（LiteLLM `model_prices.json` 或简化列表），设置页「从价格源同步」默认用它 |
| `TRAWL_ALLOW_PRIVATE_FETCH` | — | 置 `1` 才允许向私网地址拉价格源（与 judge 的私网开关分开） |

打开 [http://localhost:8080](http://localhost:8080) 进入**案件台账**。

## 上报一条链路

任何按下列 JSON 事件上报的客户端，只需改 baseUrl 与密钥：

```
curl -X POST localhost:8080/api/public/ingestion \
  -H "Authorization: Bearer $TRAWL_SECRET" \
  -H "Content-Type: application/json" \
  --data @testdata/sample.json
```

或走 OTLP/HTTP（GenAI 语义约定）：

```
curl -X POST localhost:8080/v1/traces \
  -H "Authorization: Bearer $TRAWL_SECRET" \
  -H "Content-Type: application/json" \
  --data @testdata/otlp.json
```

支持的事件类型：`trace-create`、`span-create/update`、`generation-create/update`、`event-create`、`score-create`、`sdk-log`（忽略）。

同一 `(project_id, trace_id, span_id)` 的后到事件按 `event_ts` 做字段级合并：新值覆盖、旧值不覆盖、空值不回填。

token 用量同时认 `usage`（官方 SDK 主字段）和 `usageDetails`（新写法），`usageDetails` 优先、`usage` 补键；两者都缺 `total` 时由 `input + output` 自动补齐，成本按 `model_prices` 换算。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/public/ingestion` | Tracing SDK JSON 摄取（Bearer / Basic / x-api-key） |
| POST | `/v1/traces` | OTLP/HTTP JSON |
| POST | `/api/public/scores` | 外部写入分数 |
| GET | `/api/traces` | 台账查询：`from` `to` `name` `q` `userId` `sessionId` `level` `limit` `cursor`；`q` 检索名称 / 输入 / 输出 |
| GET | `/api/traces/{traceId}` | 链路详情：`{spans, scores}` |
| GET | `/api/queue` | 待判队列：`{items, stats:{pending, judgedToday, judgedTotal}}` |
| GET | `/api/prompts` | prompt 版本台账：按 `name + version` 聚合 calls / tokens / cost / 通过率 |
| POST | `/api/scores` | 人工判词（UI 使用） |
| POST | `/api/scores/batch` | 批量判词：`{scores:[...]}`，1..500 条，单事务写入 |
| POST | `/api/judge` | 模型初判：`{traceId, spanId?}`，写 `llm_judge`，未配置 400，超预算 429 |
| POST | `/api/judge/batch` | 批量机判：`{traceIds:[...]}` 或 `{items:[{traceId,spanId}]}`，1..50 条，并发 3，逐条软失败 |
| GET | `/api/scores?traceId=` | 分数列表 |
| GET/POST | `/api/datasets` | 回归集列表 / 新建（`{name, note}`） |
| GET/DELETE | `/api/datasets/{id}` | 详情（含 `items` 与按 prompt 版本的通过率 `versions`）/ 删除 |
| POST | `/api/datasets/{id}/items` | 收入链路：`{traceIds:[...]}`，重复自动去重 |
| DELETE | `/api/datasets/{id}/items/{traceId}` | 从回归集移出 |
| GET | `/api/prices` | 价格表（`inputPer1k` / `outputPer1k`） |
| PUT/DELETE | `/api/prices/{model}` | 单模型改价 / 删价，热更新到摄取换算 |
| POST | `/api/prices/sync` | 从 `{url}` 或 `TRAWL_PRICES_URL` 拉价格源，兼容 LiteLLM per-token 形状 |
| POST | `/api/backup` | `VACUUM INTO` 生成 `<dataDir>/backups/*.db`，返回下载地址 |
| GET | `/api/backup/{name}` | 下载备份（`filepath.Base` + 只允许 `.db`） |
| GET | `/api/export/scores.csv` | 判词导出 CSV |
| GET/POST | `/api/projects` | 项目列表 / 新建（新密钥只返回一次） |
| GET | `/api/status`, `/healthz` | 运行状态 |
| GET | `/`, `/t/{traceId}`, `/queue`, `/queue/{traceId}`, `/prompts`, `/datasets`, `/settings` | 内嵌阅卷台 UI |

多项目读接口通过 `X-Trawl-Project: <projectId>`（或 `?project=`）选择项目，缺省与未知 id 都回落引导项目；摄取侧的项目由密钥决定，二者不会串。

鉴权：`Authorization: Bearer <secret>`、`Basic <public>:<secret>`、`x-api-key: <secret>`。密钥仅存 SHA-256，不可反查。

### 三种判词方式

- **单条精判**：详情页打分 + 理由，写入 `verdict`。
- **批量盖章**：队列页勾选多条，统一维度与理由，一次事务写入（`POST /api/scores/batch`）。
- **模型初判**：详情页「模型初判」按钮或队列页「整批机判」，把输入 / 输出发给 OpenAI 兼容端点，拿 0-3 分与理由，写入 `llm_judge`（source=`LLM`，`raw_response` 保留模型原文）。机判不替代人工：队列仍只认 `verdict`，模型分只是证据之一。日调用上限由 `TRAWL_JUDGE_DAILY_CAP` 硬性拦。

回归集把「同一批问题」固定成 `datasets` + `dataset_items`，再按 `prompt_name + prompt_version` 汇总条目数、已判数、通过率、平均判词，用来回答「v2 比 v1 好多少」。条目来自台账，判词仍写在 `scores`。

检索按长度分流：`q` ≥ 3 字符走 FTS5 `trigram` 短语查询（`span_fts` 是 external content 表，由触发器跟随 `spans` 同步），1-2 字符回落 `LIKE`。原因是 `unicode61` 把整句中文当一个 token，「退款」这类两字词在 trigram 下不构成 token。

prompt 版本通过率来自 `spans.prompt_name` / `prompt_version`。JSON 摄取读 `promptName` / `promptVersion`，OTLP 调用方可设置同名 SDK 属性。某一版本下所有 generation 关联的 trace 有 `verdict >= 1` 即算通过。

## 数据模型

`spans` 为统一宽表（trace / span / generation / event 同行），`scores` 存判词，`model_prices` 提供成本换算，`projects` / `api_keys` 管项目与密钥，`ingest_batches` 预留幂等批次。

迁移 `0003` 给 `spans` 加 `prompt_name` / `prompt_version`，支撑版本维度聚合；`0004` 给 `scores` 加 `raw_response`，保留模型原始输出供审计；`0005` 增加 `span_fts`（FTS5 trigram external content + 三触发器）、`datasets` 与 `dataset_items`（唯一索引 `(dataset_id, trace_id)` 兜住重复收录）；`0006` 把 `spans` 主键从 `(project_id, span_id)` 改为 `(project_id, trace_id, span_id)`：OTLP 只要求 span id 在自己 trace 内唯一，旧主键会让同名 span 被合并进另一个 trace（看不见的数据错位）。

- 时间统一 `INTEGER` 毫秒（UTC）。
- `STRICT` 表；JSON 字段以 TEXT 存 JSON，用 `json_extract` 做聚合。
- 写连接 1 条（WAL），读连接 4 条（`mode=ro`）。

## 开发

```
make test     # go test ./...
make fmt      # gofmt
make vet
make tidy
```

若默认 Go 缓存目录不可写（受限环境常见），改用项目内缓存：

```
GOPATH=$PWD/.cache/gopath GOCACHE=$PWD/.cache/go-build \
GOMODCACHE=$PWD/.cache/gopath/pkg/mod go build -o bin/trawl ./cmd/trawl
```

目录：

```
cmd/trawl            入口、配置、优雅退出
internal/store       双连接池、迁移、upsert 合并、查询、队列、prompt 聚合；extras.go 管回归集/价格/备份/项目
internal/writer      单写者批处理（150ms / 500 行）
internal/ingest      JSON 摄取 + OTLP 解析、成本换算
internal/judge       OpenAI 兼容判分客户端（私网默认拒绝、JSON 严格解析）
internal/httpapi     路由、鉴权、JSON；admin.go 管回归集/价格/备份/导出/项目
internal/web         go:embed dist 与静态资源
internal/web/dist    Vite 构建产物（随仓库提交，保证 go build 单命令可用）
web/                 React + Vite + TS 源码（src/pages：台账 / 阅卷 / 队列 / Prompt 版本 / 回归集 / 设置）
web/legacy           M1 的单文件 UI，仅作降级参考
testdata             示例载荷
```

前端改动流程：

```
cd web && npm install
npm run dev        # :5173，/api 与 /v1 代理到 :8080
make web-build     # 构建到 internal/web/dist，再 go build 即可嵌入
```

## 路线图

- **M1（已完成）**：摄取→落库→台账→详情→判词，单项目，单文件 SQLite。
- **M2（已完成）**：React/Vite 四屏（台账 / 阅卷 / 队列 / Prompt 版本）、FIFO 评测队列、批量判词、prompt 版本聚合。
- **M3（已完成）**：LLM-as-judge 单条与批量初判、日预算护栏、原始响应落库、回归集与版本通过率对比、价格表与价格源同步、备份、CSV 导出、多项目与项目切换。
- **M4（部分完成）**：FTS5 trigram 全文检索（≥3 字符）、备份 / 导出已落地；待做 RBAC 细粒度与 Postgres 模式（PostgreSQL 明确不实现，SQLite 单文件是产品前提）。

## 规模与性能（实测）

单文件 SQLite、单机 macOS，HTTP 端到端延迟（3 次取样）：

| 数据规模 | 列表 | 队列 | 全文检索（≥3 字，FTS） | 检索（2 字，LIKE） | Prompt 版本 |
|---|---|---|---|---|---|
| 2 万 span / 1 万 trace | 40ms | 72ms | 18ms | 18ms | 16ms |
| 10 万 span / 5 万 trace | 210-330ms | 410ms | 65ms | 73ms | 34ms |

写入：2 万 span 批量摄取 0.7s、8 万 span 追加 3.1s（约 2.6 万 span/s，含 FTS 索引维护）。库文件：2 万 span 9.2MB、10 万 span 45MB。

一条硬约束：trace 聚合 SQL 里不能出现「按 trace 回查某一行」的相关子查询。`GROUP BY` 语境下 SQLite 无法下推 `trace_id` 相关性，分组数一多就退化成每组一次全表扫描（1 万 trace 时单条聚合 25.5s、列表 39s、队列 78s）；改成纯聚合表达式后同一条 SQL 0.027s。

## 已知限制

- 多项目已可用，但密钥 / 权限不分级：任何持有 UI 访问权的人都能切换项目。
- 回归集只做「按版本对比通过率」，不做自动重放（不重跑 LLM，只汇总既有判词）。
- 价格同步只认 LiteLLM `model_prices.json` 与简化列表两种形状，其他账单格式需先转一次。
- 写缓冲满时返回 `429`，不丢数据的前提是客户端重试。
- UI 读接口未鉴权，仅适合本机 / 内网；对外暴露请置于反向代理后。
- 成本只在 `model_prices` 命中时计算，未知模型不猜价格。
- 模型初判按 span 的 input/output 打分，不做多轮对话重放；判分提示词固定，暂不支持自定义 rubric。
- 同一 trace 可重复机判，`scores` 保留全部历史；详情页只显示最新一次，历史上限靠 `TRAWL_JUDGE_DAILY_CAP` 控制。

## License

MIT，见 [LICENSE](LICENSE)。
