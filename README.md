# jc_proxy

一个面向多供应商 LLM API 的轻量代理服务，支持按供应商路由、上游 Key 轮询与熔断、管理后台和配置热更新。

## 功能概览

- 按路径转发到不同上游，例如 `/openai/...`、`/anthropic/...`
- 支持 `round_robin`、`random`、`least_used`、`least_requests`，以及 `lowest_latency`、`highest_success`、`adaptive` 三种性能感知 Key 策略
- 每个托管 Key 缓存最近 5 次尝试的头耗时/整体耗时，均值随现有统计批量持久化；密钥中心单行显示 `响应/耗时 1s/30s`（详情在悬浮提示），支持手机卡片布局
- 按显式错误规则禁用 Key、退避、故障切换与屏蔽；空策略仅统计并透传
- 支持流式响应转发
- 支持原始供应商的请求插件：Go 原生接口优先，可选 JavaScript 脚本，见 [插件配置与接口](docs/gateway-plugins.md)
- 支持管理后台、上游 Key 存储和运行时配置更新
- 支持文件或 PostgreSQL 存储

## 目录结构

```text
cmd/jc_proxy/           程序入口
cmd/jc_proxy_upgrade/   数据结构升级命令
internal/gateway/       网关转发与 Key 策略
internal/admin/         管理后台与配置管理
internal/keystore/      上游 Key 存储
internal/config/        配置加载与校验
web/                    管理后台前端源码
config.example.yaml     示例配置
Makefile                构建、测试与开发命令
```

## 快速开始

1. 准备配置文件

```bash
cp config.example.yaml config.yaml
```

2. 按需修改 `config.yaml`

- 在 `vendors` 数组里配置每个供应商的 `name` 和 `upstream.base_url`（`id` 可省略，首次启动自动生成）
- 配置客户端鉴权 `client_auth`
- 配置上游鉴权 `upstream_auth`
- 根据需要启用 `admin.enabled`

3. 启动服务

```bash
make run CONFIG=./config.yaml
```

默认监听地址由 `server.listen` 决定，示例配置中为 `:8092`。

## 构建与开发

需要 Go 1.26、Node.js/npm 和 GNU Make。`make build` 每次都会重新构建前端，再将最新资源嵌入网关二进制。前端依赖仅在首次执行或依赖清单变更时按锁文件安装。

```bash
make build          # 生成 bin/jc_proxy 和 bin/jc_proxy_upgrade
make test           # Go + 前端测试
make test-race      # Go 全量 race 检查
make check          # 测试 + go vet
make bench-plugins  # 插件性能基准
make clean          # 清理构建产物
make help           # 查看全部常用命令
```

例如 `make bench-plugins BENCHTIME=1s BENCH_COUNT=5` 可调整基准参数。`make build` 支持 `-j` 并行构建；`make clean` 保留配置文件、运行数据和已安装的前端依赖。

## Render 部署

`render.yaml` 现在使用“无本地配置文件”的启动方式：

- 启动命令直接使用 `./bin/jc_proxy`
- 通过 `DATABASE_URL` 和 `JC_PROXY_STORAGE_MODE=pgsql` 连接 PostgreSQL
- 运行时配置保存在 `jc_proxy_configs` 表
- 上游 Key 保存在 `jc_proxy_upstream_keys` 表

首次在 Render 启动时，若库中还没有配置记录，服务会用环境变量生成一份 bootstrap 配置并写入 PGSQL。

> **数据库 schema 必须与部署的二进制一致。**本仓库对应的 Neon 数据库已是 v3。若 Render 上仍是 v2 构建，唤醒/重新部署时会因 `schema version 3 is newer than this binary supports (2)` 拒绝启动；旧构建也**绝不能再执行配置保存**，否则会把 v2 载荷写回数据库行。请部署包含 v3 支持的构建；版本规则见 [架构与升级](docs/architecture.md#4-数据结构版本与升级)。

- 默认会开启管理后台：`JC_PROXY_ADMIN_ENABLED=true`
- 若未显式提供管理员密码，首次启动会自动生成随机密码并打印到 Render 日志
- 此时即使还没有任何 vendor 配置，服务也能先启动，你可以登录 `/console/` 后再补充供应商和上游 Key
- 若前面还有反向代理或负载均衡，请同时配置 `admin.trusted_proxy_cidrs` 或 `JC_PROXY_ADMIN_TRUSTED_PROXY_CIDRS`，否则后台来源 IP 限制只会看到代理地址

常用的 Render 环境变量：

```text
DATABASE_URL
JC_PROXY_STORAGE_MODE=pgsql
JC_PROXY_ADMIN_ENABLED=true
JC_PROXY_ADMIN_TRUSTED_PROXY_CIDRS=10.0.0.0/8
JC_PROXY_STORAGE_CONFIG_PGSQL_TABLE=jc_proxy_configs
JC_PROXY_STORAGE_CONFIG_PGSQL_RECORD_KEY=default
JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_TABLE=jc_proxy_upstream_keys
```

## 请求示例

OpenAI 风格请求：

```bash
curl http://127.0.0.1:8092/openai/v1/chat/completions \
  -H 'Authorization: Bearer client-key-a' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'
```

Anthropic 风格请求：

```bash
curl http://127.0.0.1:8092/anthropic/v1/messages \
  -H 'Authorization: Bearer client-key-a' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-sonnet-latest","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}'
```

## 管理后台

启用 `admin.enabled: true` 后：

- 后台入口：`/console/`
- 健康检查：`/healthz`

如果 `admin.password` 和 `admin.password_hash` 都为空，首次启动会自动生成初始密码并打印到日志。

管理后台还有两条默认安全行为：

- 管理员密码轮换后，现有后台会话会立即失效，需要重新登录
- 登录连续失败会触发限流与审计记录，避免后台被无限爆破

## 配置说明

主要配置可参考 [config.example.yaml](config.example.yaml)：

- `server`: HTTP 服务监听与超时配置
- `admin`: 管理后台账号、会话和访问控制
- `storage`: 配置与上游 Key 的存储方式
- `vendors`: 每个供应商的上游地址、鉴权、路径重写和错误策略
- `schema_version`: 持久化数据结构版本，见下节

### 客户端鉴权与未配置防护

所有供应商（包括聚合入口、passthrough 模式）默认开启 `client_auth.enabled`，省略或 `null` 均视为开启：

| 客户端密钥 | `enabled` | 公开接口行为 |
|---|---|---|
| 未配置、空列表或仅空白 | 任意 | 拒绝访问（401），不向上游发请求；可正常保存配置和继续在后台配置 |
| 已配置非空密钥 | 省略 / `true` | 必须携带有效 `Authorization: Bearer ...` 或 `X-API-Key` |
| 已配置非空密钥 | 显式 `false` | 跳过客户端鉴权，允许匿名访问 |

删除最后一条客户端密钥会重新开启鉴权并关闭接口访问；再次添加密钥后默认需要鉴权。
这是**客户端密钥**防护，上游 Key 不能替代客户端密钥。聚合请求在聚合入口鉴权，内部转发不重复校验 child 的客户端密钥；child 的公开入口仍独立受保护。
升级后，原来没有配置客户端密钥的公开接口将停止放行；需要配置密钥后使用鉴权，或明确关闭鉴权。已配置密钥且显式关闭的供应商保持免鉴权。

### 供应商 ID 与改名

`vendors` 是有序数组，每一项都有 `id` 和 `name` 两个标识：

```yaml
vendors:
  - id: "v_75fc384a04f64dcf"   # 不可变，手写配置时可省略，启动时自动生成并写回（写回会重新序列化配置，文件内注释不保留）
    name: "openai"              # 可改，同时是客户端调用路径的第一段
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
```

- `id` 是唯一标识：上游 Key 分区、运行统计、聚合供应商的 `aggregate.children[].vendor_id` 全部绑定在它上面
- `name` 只是展示名 + 路由段，可以随时改名，存储的数据一条都不用搬

改名可以在后台供应商详情页操作，或直接调接口：

```bash
curl -X POST http://127.0.0.1:8092/admin/vendors/v_75fc384a04f64dcf/rename \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"openai-prod"}'
```

唯一的外部影响是调用地址：改名后 `/openai/v1/...` 立即返回 404，客户端要改成 `/openai-prod/v1/...`。
名称必须全局唯一，不能包含 `/ ? # % \` 或空格，也不能占用内部路由 `healthz`、`console`、`admin`。

所有供应商维度的管理接口都按 `id` 寻址（`/admin/vendors/{id}`、`/admin/upstream-keys/{id}`），
因此并发改名不会让后台操作打到错误的供应商。

### 最近 5 次耗时与 Key 负载策略

托管 Key 的每次上游尝试（包括故障转移）在内存固定窗口（5 条）中采样，**不保存响应内容，不逐请求写数据库**：

- **头耗时**：从发出上游请求到收到响应头，含连接、TLS、上传及等待；不是首 token 耗时。**整体耗时**计到响应体 EOF，流式响应计到流结束，含下游背压。
- **失败惩罚**：HTTP 错误（含 502/504）、网络失败、上游超时/断流、异常空响应，每次按 **`999s/999s`** 计入窗口均值——这是惩罚值，不是真实延迟；混合成功/失败的窗口仍求平均。
- **中断**：客户端取消、上传读取失败及下游写失败计入累计请求，但不计上游失败、不进窗口、不改变 Key 健康；界面单独显示中断次数，成功率分母只有成功与上游失败。
- 均值按 **5 秒批量刷盘**持久化，原始样本只在内存；进程重启恢复持久化均值作初始参考，随后用新请求重建窗口。

完整的判定规则（空响应、取消时序）、采样字段与持久化边界见 [耗时统计与 Key 负载策略](docs/performance-and-balancing.md)。

在普通供应商的 `load_balance` 配置（聚合供应商应配置在其 child 上）：

| 策略 | 侧重点 | 建议 |
|---|---|---|
| `lowest_latency` | 头耗时、近期成功率、当前并发 | 流式对话、重视快速开始响应 |
| `highest_success` | 平滑近期成功率、当前并发 | 稳定性优先 |
| `adaptive` | 头耗时、更强的成功率惩罚、当前并发 | 兼顾快速响应和稳定性，适用于流式及不同输出长度 |

整体耗时仍展示并持久化，但不再参与评分，避免输出长度和下游背压影响延迟比较。

默认保留约 10% 选择预算探索无样本或过期的 Key（探索失败退避 1/2/4/5 分钟，成功后清除），旧评分在最后一次采样后 5–10 分钟回归中性值。供应商选择三种性能策略之一后，「上游配置 → Key 动态评分」可调整 TTL、衰减时长、探索间隔/退避、成功率指数、并发权重及无样本初始耗时；保存后热更新，支持恢复默认。配置对应 `vendors[].performance`，省略沿用原默认值，切换到普通策略保留设置但不应用。详细范围、YAML 示例与场景建议见 [耗时统计与 Key 负载策略](docs/performance-and-balancing.md#供应商级参数)。

调度耗时只使用成功样本，窗口全失败的 Key 会被大幅降权，而不是因为报错快就获得更多流量。默认仍为 `round_robin`，不会自动更改已有供应商策略。

响应链路的关键边界：`102 Processing` 保活不会提前提交最终响应状态；SSE 首块头数据一次刷新；错误摘要只解析一次。`body_timeout` 仅约束上游读取等待，慢下游不会误伤健康 Key；未读完的遮罩错误体最多清理 64KiB / 50ms。`upload_timeout` 约束非空请求体的上传阶段，上传期间客户端断开按中断处理、不惩罚 Key。`base_url` 查询参数在前、客户端原始查询在后并保留重复参数，路径保留 `%2F` 等转义。内置认证标识的自动禁用只匹配结构化错误码，避免用户输入反射误禁用 Key。完整清单见 [网关链路行为](docs/gateway-behavior.md)。

### 后台重置运行统计

在 **密钥中心** 点击“重置统计”（当前供应商）或“全部重置”，在普通确认框中确认即可。重置范围包含该供应商的所有 Key，不受搜索、筛选或分页影响；全部重置也包含禁用 Key 和未配置的存储分区。操作不可撤销，重要历史数据应事先备份。

- 清空累计请求/成功/分类错误计数、最近状态/错误、最近五次性能窗口及评分缓存；`least_requests` 从零累计，性能策略重新学习。
- 同步清理存储、共享内存统计及刷盘基线，避免下一次 Flush、路由刷新、删除后重新添加 Key 或重启恢复旧统计。未选中的供应商仍正常累计。
- **保留 Key、备注、启用/禁用状态、冷却/退避、配置和在途数**。不会主动中断请求；重置后提交的计数/样本（包括此前开始的请求）归入新一轮，因此刷新时不保证一直显示零。统计提交在持久化清零期间可能短暂等待。
- 记录 `runtime_stats.reset` 审计事件（操作者、范围、供应商 ID、Key 数）；明确的存储失败不提交内存重置，记录 `runtime_stats.reset_failed`。
- **只协调当前网关进程**。多个实例共享存储时，必须先停止其他实例并完成它们的最终刷盘，再在保留的实例中重置，之后重新启动其他实例。不要在运行中直接修改 JSON/数据库，也不要同时保留旧实例写入。
- PostgreSQL 使用单条原子 UPDATE（最长等待 10 秒），无需升级表结构。网络中断等情况下仍可能无法确认数据库是否已提交；此时先核实存储状态，必要时停止所有实例、确认或重新清零后再启动，不要盲目反复点击。

管理 API：`POST /admin/stats/reset`，沿用管理员认证与来源 IP 限制：

```json
{"scope":"vendor","vendor_id":"vid_example","confirmation":"RESET"}
```

全部重置使用 `{"scope":"all","confirmation":"RESET"}`，不可携带 `vendor_id`。成功返回 `{"ok":true,"count":12}`，`count` 为范围内的持久化 Key 数。聚合供应商应选择实际 child 供应商或全部重置；仅重置某个 child 也会影响引用它的其他聚合路由。

### 配置更新与运行状态一致性

- 管理端串行化服务内的配置/Key 变更。配置先完整构建路由，再持久化，成功后才发布；保存失败不切换路由或清空会话。
- `pgsql` 模式的运行时配置更新只提交权威数据库，不再同时回写本地 bootstrap 文件；避免第二次写失败造成分叉。
- 保留的 Key 在新旧路由间共享并发、冷却、退避及禁用状态。新版持久化状态优先；迟到的旧版本结果只更新累计计数，不覆盖新健康状态/最近错误。
- 文件 Key 操作同样先成功写盘再提交内存，失败不会消耗条件更新版本号。
- 状态同步日志显示 KeyID，Key 本身（包括驱动错误中回显的 Key）脱敏。

以上保障为单进程范围。完整配置替换仍是替换语义，不提供跨客户端/跨实例 CAS；批量 Key 操作也不是跨 Key 事务。
实现细节见 [架构与升级](docs/architecture.md)。

### 数据结构版本与升级

配置和上游 Key 存储都带版本标记（配置里的 `schema_version`，PGSQL Key 表旁的 `<table>_meta` 表），当前版本为 v3。
服务只读取当前版本，遇到旧数据会直接拒绝启动并提示升级，不会在线迁移：

```text
load config failed: config schema version 2 is older than required (3): run "jc_proxy_upgrade -config <path>" to migrate, then restart
```

升级前先备份并停止所有实例，避免旧版与新版混跑；升级用独立命令，先 dry-run 再执行：

```bash
go build -o jc_proxy_upgrade ./cmd/jc_proxy_upgrade
./jc_proxy_upgrade -config ./config.yaml -dry-run
./jc_proxy_upgrade -config ./config.yaml
```

- 升级命令会自动备份原配置（文件或数据库行），可重复执行：已是当前版本会跳过，中途失败也能重跑。
- `storage.config.driver=pgsql` 时会同时迁移本地文件和数据库里的配置副本，避免重启读到旧数据。
- 升级后先在 `web/` 执行 `npm ci && npm run build`，再重新构建并启动服务；不要只修改 `schema_version` 跳过迁移。
- 各版本的变更内容、备份与回退规则见 [架构与升级](docs/architecture.md#4-数据结构版本与升级)。

`storage` 是启动前配置，优先级为：

- 进程环境变量
- 配置文件同目录的 `.env`
- `config.yaml`

运行时数据库中的配置不会反向覆盖 `storage`。如果要切换存储方式，例如从 `file` 改成 `pgsql`，需要修改环境变量或配置文件后重启服务。

如果要把配置存储和上游 Key 存储都切到 PGSQL，最简单的方式是设置：

```text
DATABASE_URL=postgres://user:pass@127.0.0.1:5432/jc_proxy?sslmode=disable
JC_PROXY_STORAGE_MODE=pgsql
JC_PROXY_STORAGE_CONFIG_PGSQL_TABLE=jc_proxy_configs
JC_PROXY_STORAGE_CONFIG_PGSQL_RECORD_KEY=default
JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_TABLE=jc_proxy_upstream_keys
```

其中：

- `JC_PROXY_STORAGE_MODE=pgsql` 会同时把 `storage.config.driver` 和 `storage.upstream_keys.driver` 设为 `pgsql`
- `DATABASE_URL` 会同时作为配置存储和上游 Key 存储的 PGSQL DSN
- 如需分别指定，可改用 `JC_PROXY_STORAGE_CONFIG_PGSQL_DSN` 和 `JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_DSN`

也可以直接写在配置文件中：

```yaml
storage:
  config:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://user:pass@127.0.0.1:5432/jc_proxy?sslmode=disable"
      table: "jc_proxy_configs"
      record_key: "default"
  upstream_keys:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://user:pass@127.0.0.1:5432/jc_proxy?sslmode=disable"
      table: "jc_proxy_upstream_keys"
```

对流式接口，建议保持：

```yaml
server:
  write_timeout: 0s
```

这样可以避免长连接在固定时长后被服务端主动切断。

对非流式长响应，供应商可配置：

```yaml
vendors:
  - name: "openai"
    upstream:
      interim_response_interval: 30s
```

网关会在最终响应开始前发送 HTTP `102 Processing` 作为保活，不会写入最终响应体；设为 `0s` 可关闭。
网关等待上游响应头的默认超时是 `300s`，如需等待更久可调大 `response_header_timeout`，或显式设为 `0s` 关闭该超时。

`upstream.upload_timeout` 默认 `300s`（`0s` 禁用），仅约束非空请求体的发送阶段，不是整个 HTTP 请求的总超时；客户端输入读取另受 `server.read_timeout` 约束。完整的语义与中断归因见 [网关链路行为](docs/gateway-behavior.md#6-上传期限与中断归因)。

`server.shutdown_timeout` 是优雅停机等待时间。到期后程序关闭剩余连接并等待请求收尾，最后刷盘；它不是无条件强退的整个进程退出上限。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
(cd web && npm test && npm run build)
```

PostgreSQL 集成测试必须显式提供**测试库** DSN（创建独立临时命名的测试表；未设置时跳过）：

```bash
JC_PROXY_TEST_PG_DSN='postgres://user:pass@localhost/testdb?sslmode=disable' \
  go test ./cmd/jc_proxy_upgrade -run TestPostgresRecentStatsMigrationAndPersistence -v
```

## 备注

- 示例配置中的上游 Key 建议改为通过外部存储维护，不要直接提交到仓库
- 默认数据文件会写入 `./data/`
- 前端源码在 `web/`，运行时使用内嵌静态资源
- 若使用 `admin.allowed_cidrs` 且服务部署在代理后，请务必同步配置 `admin.trusted_proxy_cidrs`
