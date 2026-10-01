# jc_proxy

一个面向多供应商 LLM API 的轻量代理服务，支持按供应商路由、上游 Key 轮询与熔断、管理后台和配置热更新。

## 功能概览

- 按路径转发到不同上游，例如 `/openai/...`、`/anthropic/...`
- 支持 `round_robin`、`random`、`least_used`、`least_requests`，以及 `lowest_latency`、`highest_success`、`adaptive` 三种性能感知 Key 策略
- 每个托管 Key 缓存最近 5 次尝试的响应头/整包耗时，均值随现有统计批量持久化；密钥中心单行显示首包/整包耗时 `1s/30s`（详情在悬浮提示），支持手机卡片布局
- 内置错误分类、自动 cooldown、自动禁用失效 Key、故障切换
- 支持流式响应转发
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
go run ./cmd/jc_proxy -config ./config.yaml
```

默认监听地址由 `server.listen` 决定，示例配置中为 `:8092`。

## Render 部署

`render.yaml` 现在使用“无本地配置文件”的启动方式：

- 启动命令直接使用 `./bin/jc_proxy`
- 通过 `DATABASE_URL` 和 `JC_PROXY_STORAGE_MODE=pgsql` 连接 PostgreSQL
- 运行时配置保存在 `jc_proxy_configs` 表
- 上游 Key 保存在 `jc_proxy_upstream_keys` 表

首次在 Render 启动时，若库中还没有配置记录，服务会用环境变量生成一份 bootstrap 配置并写入 PGSQL。

> **数据库 schema 必须与部署的二进制一致。**本仓库对应的 Neon 数据库已于 2026-10-01 升级到 v3。若 Render 上仍是 v2 构建（仓库尚未包含 v3 源码），唤醒/重新部署时会因 `schema version 3 is newer than this binary supports (2)` 拒绝启动；旧构建也**绝不能再执行配置保存**，否则会把 v2 载荷写回数据库行。请先推送含 v3 的提交并重新部署，详见 [v3 优化与升级记录](docs/v3-upgrade.md)。

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

主要配置可参考 [config.example.yaml](/home/xjc/jc_proxy/config.example.yaml)：

- `server`: HTTP 服务监听与超时配置
- `admin`: 管理后台账号、会话和访问控制
- `storage`: 配置与上游 Key 的存储方式
- `vendors`: 每个供应商的上游地址、鉴权、路径重写和错误策略
- `schema_version`: 持久化数据结构版本，见下节

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

托管 Key 的每次上游尝试（包括故障转移）在内存固定窗口中采样，**不保存响应内容，不逐请求写数据库**：

- **首包/响应头耗时**：从开始上游请求到收到最终响应头，含连接、TLS、上传及等待；不是首 token 耗时。
- **整包耗时**：从同一起点到响应体读至 EOF；流式响应计到流结束，含下游背压。未读完整的错误/重试响应不伪装成完整样本。
- **成功率**：最近最多 5 次有效尝试的成功比例；客户端取消不惩罚上游。缺少耗时样本显示 `—`。
- 沿用 **5 秒批量刷盘**、仅变化时写入、关闭时最终刷盘。累计计数累加，窗口均值/样本数覆盖，不能把均值当增量相加。
- 原始 5 条样本只在内存；路由热更新保留窗口，进程重启恢复持久化均值作初始参考，随后用新请求重建窗口。

在普通供应商的 `load_balance` 配置（聚合供应商应配置在其 child 上）：

| 策略 | 侧重点 | 建议 |
|---|---|---|
| `lowest_latency` | 响应头耗时、近期成功率、当前并发 | 流式对话、重视快速开始响应 |
| `highest_success` | 平滑近期成功率、当前并发 | 稳定性优先 |
| `adaptive` | 响应头与整包耗时、成功率、当前并发 | 请求大小相近的非流式业务 |

新策略用 10% 选择预算探索：无样本 Key 补足 2 次有效样本，5 分钟没有新样本的 Key 重新进入探索队列，优先选择最久没获得尝试机会的 Key。额度最多积累一次，跨白名单请求及热更新保留；有在途请求的 Key 不再追加探索请求。失败后的探索间隔为 1/2/4/5 分钟，成功后清除退避。探测严格遵守禁用、冷却、重试排除和 Key 白名单。
调度耗时仅使用成功样本，展示均值仍包含所有有耗时的响应。旧评分在最后一次采样后 5–10 分钟逐渐回归中性值；过期后的新样本重建窗口，旧摘要在没有新样本时继续展示。探索使用实际请求，不额外调用上游；无合适流量时不保证按分钟更新。
默认仍为 `round_robin`，不会自动更改已有供应商策略。窗口全失败的 Key 会被大幅降权，而不是因为报错快就获得更多流量。
具体公式、性能对照、存储边界与后续网关优化见 [性能与负载策略报告](docs/performance-and-balancing.md)。

### 配置更新与运行状态一致性

- 管理端串行化服务内的配置/Key 变更。配置先完整构建路由，再持久化，成功后才发布；保存失败不切换路由或清空会话。
- `pgsql` 模式的运行时配置更新只提交权威数据库，不再同时回写本地 bootstrap 文件；避免第二次写失败造成分叉。
- 保留的 Key 在新旧路由间共享并发、冷却、退避及禁用状态。新版持久化状态优先；迟到的旧版本结果只更新累计计数，不覆盖新健康状态/最近错误。
- 文件 Key 操作同样先成功写盘再提交内存，失败不会消耗条件更新版本号。
- 状态同步日志显示 KeyID，Key 本身（包括驱动错误中回显的 Key）脱敏。

以上保障为单进程范围。完整配置替换仍是替换语义，不提供跨客户端/跨实例 CAS；批量 Key 操作也不是跨 Key 事务。
本地升级记录见 [v3 优化与升级记录](docs/v3-upgrade.md)。

### 数据结构版本与升级

配置和上游 Key 存储都带版本标记（配置里的 `schema_version`,PGSQL Key 表旁的 `<table>_meta` 表）。
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

v1 → v2 会做这些事：

- `vendors` 从按名字索引的 map 改成 `[{id, name, ...}]` 数组，并为每个供应商生成 `id`
- `aggregate.children[].vendor` 改成 `vendor_id`
- 上游 Key 分区从供应商名字改成 `id`（PGSQL 是 `vendor` 列改名为 `vendor_id`，在单个事务内完成）
- 移除早已废弃的 `vendors.*.upstream.keys` 内联 Key 字段

v2 → v3 新增 PGSQL Key 表的 `recent_stats JSONB`，保存近期均值和样本数；添加列与 Key 表版本标记在同一事务内完成。配置结构与 vendor ID 不变，仅更新版本标记；文件 Key 存储保留原分区、Key 和累计计数。新安装直接创建 v3。

配置文件和 PGSQL 配置行都会先备份（`config.yaml.v<旧版本>.<时间戳>.bak` / `<record_key>.v<旧版本>.backup.<时间戳>` 行）。
命令可以重复执行：已经是 v3 的数据会跳过，中途失败也能重跑。迁移工具拒绝覆盖比自身更新的 Key schema。
升级后先在 `web/` 执行 `npm ci && npm run build`，再重新构建并启动服务；不要只修改 `schema_version` 跳过迁移。
`storage.config.driver=pgsql` 时会同时迁移本地文件和数据库里的配置副本，避免重启读到旧数据。

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
