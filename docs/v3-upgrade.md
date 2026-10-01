# v3 优化与升级记录

日期：2026-10-01。承接 [初轮性能分析](performance-and-balancing.md)。

## 已落实的优先优化

### 1. 配置更新的一致性

`Service.changeMu` 覆盖配置 RMW 的全过程，配置/Key 管理操作串行，防止两个创建供应商或添加客户端 Key 操作相互覆盖。
读取和数据面转发不等待这把锁。

`Runtime.UpdateAndPersist` 的顺序：

1. 克隆、校验并构建完整候选路由。
2. 调用配置存储持久化；失败则保留原路由和健康状态，清理候选 Transport。
3. 绑定共享 Key 状态、发布新路由和配置，不在发布后执行可能失败的持久化。

`RefreshKeys` 在同一更新锁内读取配置，避免一个等待中的刷新把较新的配置替换回旧版本。
路由构建阶段不再合并/修改已有统计 handle，失败准备不会提前恢复冷却中的 Key。

配置文件使用随机临时文件、`Sync`、原子 rename，消除固定 `.tmp` 文件被并发写者争用的问题。
PGSQL 模式的**运行时配置更新**只写权威数据库，本地文件保持 bootstrap 身份，不再在 DB 成功后因镜像文件失败而返回错误。

此外，文件 Key 存储的增删、替换、状态、备注都改为先保存新数据再提交内存。
特别是条件自动禁用：写文件失败不会提前增加内存版本号，重试不会因此被误判为版本冲突。

### 2. Key 状态跨路由重建共享

Pool 保留独立选择策略/游标，但保留的 Key 引用同一 `KeyState`，重叠的路由代次共用该供应商的锁。
不再用 Snapshot 抢救状态，因此：

- 新路由能看到旧请求的 inflight；旧请求结束时正确减回，不会归零或永久泄漏。
- 重建之后才返回的失败、冷却、禁用仍被当前路由看到。
- 同版本的旧存储快照不能覆盖刚刚发生的自动禁用。
- 新的持久化版本（如管理员启用/恢复）优先，清理旧冷却/退避。
- 选择 Key 与捕获其版本在同一临界区，防止旧请求被标记成新版本。
- 迟到的旧版本响应仍计入累计计数，但不改变新健康状态/最近错误，也不污染新版本的近期性能窗口。
- 已删除 Key 在旧池中退休，不再允许旧路由重试它；再次添加使用新健康状态。

这些是进程内运行状态，不额外逐请求写数据库。进程重启仍会清空未持久化的冷却/并发状态。

### 3. 日志脱敏与失败可见性

异步状态同步的正常重试与最终关闭错误统一使用 `key_id`，并移除底层错误消息中回显的明文 Key。
错误包装保留 `errors.Is/As` 能力。版本冲突/Key 已删除仍停止重试，但现在会留下脱敏诊断，不再静默丢弃。

## 验证

通过：

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build` 服务及升级工具
- 前端 `npm test`、`npm run build`

新增回归覆盖：24 个并发控制面变更、配置保存失败不发布、单一远端提交点、并发文件写入、候选路由准备失败无副作用、刷新不恢复旧配置、跨代并发归还、晚到冷却/禁用、手动恢复后的旧版本保护、删除后重新添加、八类文件 Key 写入失败后重试及日志脱敏。

本机新增修改后的微基准：100 Key 下新策略 acquire/release 约 1.06–1.10μs、零分配；round-robin 约 175ns、零分配。
假 Transport JSON/SSE 转发约 2.58μs/2.50μs，均 45 alloc/op。没有为了共享状态引入每请求 goroutine 或额外分配。

## 已执行的升级范围

默认 `config.yaml` 指向 **file/file**，升级前未发现本机运行中的 `jc_proxy`。首先升级这套默认存储：

| 对象 | 结果 |
|---|---|
| `config.yaml` | schema 2 → 3，验证除版本之外的解析数据完全相同 |
| `data/upstream_keys.json` | schema 2 → 3，分区及记录保持不变（原文件为空 Key 集） |
| `bin/jc_proxy` | 已替换为包含最新后端和嵌入前端的新版构建 |
| `bin/jc_proxy_upgrade` | 已构建并执行，重复 dry-run 确认已是 v3 |

执行顺序：私有备份 → dry-run → 校验 dry-run 未改数据 → 正式迁移 → 对比迁移前后非版本数据 → 再次 dry-run → 本地启动验证。

启动验证使用配置副本、Key 文件副本和随机 loopback 端口，不修改默认数据、不访问真实上游：
`/healthz` 返回 200/ok、嵌入 `/console/` 返回 200，随后 SIGTERM 优雅退出并最终 Flush。
**没有启动常驻服务，保留升级前停止状态。**

### 后续追加：Neon PostgreSQL 升级

经用户明确追加授权，已升级 `migrate_neon.yaml` 指向的 Neon PostgreSQL 18。配置和 Key 存储使用同一数据库的直连端点；多次检查未发现其他客户端连接，但这不能证明远端应用已停止或已部署新版。

| 对象 | 结果 |
|---|---|
| `migrate_neon.yaml` | schema 2 → 3，其他解析数据不变 |
| `jc_proxy_configs` 的 `default` 行 | schema 2 → 3，45 个供应商的 ID 及全部非版本配置不变 |
| `jc_proxy_upstream_keys_meta` | `schema_version` 从 2 改为 3 |
| `jc_proxy_upstream_keys` | 新增 `recent_stats JSONB NOT NULL DEFAULT '{}'::jsonb`，298 行原有字段逐行完全一致 |

校验保留：33 个 Key 分区、330,787 次累计请求、263,020 次成功；状态分布保持 active 251、disabled_manual 45、disabled_auto 2。Key 明文值、备注、版本、时间戳、禁用原因及所有原有统计均保持不变，新增近期统计全部初始化为 `{}`。原索引和约束保留；PostgreSQL 18 会为新增列自动登记 `NOT NULL recent_stats` 约束，这属于预期 DDL。

执行顺序：只读预检 → 私有逻辑备份 → 正式目标 dry-run 并核实无改动 → 独立测试 schema 验证 → 数据库内额外备份 → 正式升级 → 逐行核对 → 再次正式执行并核实无变化。

追加验证：

- 在独立临时 schema 中运行 `TestPostgresRecentStatsMigrationAndPersistence`，v1/v2 → v3、dry-run、重复升级、未来版本拒绝、计数与 JSONB 快照写入/Replace/List 均通过；只使用合成测试 Key，没有把生产表用于测试写入，临时 schema 已删除。
- 用显式只读事务读取真实配置及全部 298 条 Key，使用项目的配置校验、记录类型/JSONB 解码和 `gateway.NewRuntime` 构建完整路由，进程内 `/healthz` 返回 200；未开启监听器、未启动统计写入器、未访问真实上游。
- 两次正式执行之后，配置文件、数据库行和 DDL 的幂等性检查通过；只读运行态检查后再次确认数据库未变。

**Neon 数据库已升级，不等于远端应用已部署/重启。**本次未操作 Render 或其他远端应用部署，也未启动本地常驻服务；非默认的 `test_config.yaml` 仍未升级。

### 远端 Render 部署状态与前置条件

`render.yaml` 部署的 `jc-proxy`（singapore，使用 `DATABASE_URL`）会读写本次升级的 `jc_proxy_configs` / `jc_proxy_upstream_keys`，与 `migrate_neon.yaml` 指向同一套 schema。升级前后检查 `pg_stat_activity` 都没有发现其他客户端连接，说明当时没有远端实例连着这个库。

2026-10-01 探测 `https://jc-proxy.onrender.com/healthz`，多次请求均返回 **HTTP 503（页面标题 `Service Suspended`）**；该服务处于挂起状态，没有在提供流量，因此当前不存在旧版实例持续写入数据库的问题。

但恢复/重新部署前必须先推送 v3 源码：仓库 HEAD（`3606f93`）不包含 v3 改动，Render 从 Git 仓库构建，用旧代码会：

- 启动即拒绝：`schema version 3 is newer than this binary supports (2)`；
- 如果旧进程能运行（例如升级前已加载内存配置），管理员保存配置会把 v2 载荷写回 `default` 行，相当于**降级数据库版本**，必须避免。

恢复步骤：提交并推送包含 v3 的改动 → 确认 Render 构建使用支持 v3 的提交 → 再用 `jc_proxy_upgrade -dry-run` 确认数据库已是 v3 → 启动/唤醒服务。数据库已经迁移完成，重新部署只需构建新版二进制，不需要再跑一次迁移。

## 备份与回退

主备份目录（私有权限）：

`/home/xjc/jc_proxy-backups/v3-20261001T035900Z/`

包含旧 `config.yaml`、旧 `upstream_keys.json`、旧二进制 `jc_proxy.v2`、SHA256 清单、迁移前工作树快照，以及 dry-run/迁移/复查日志。
升级工具另在原文件旁生成 `*.v2.20261001T040010Z.bak`；这些文件和主备份都包含敏感配置，不应提交或分享。

若回退，先停止服务，再从同一备份恢复配置、Key 文件和旧二进制，必须成套恢复，不能只改 schema 数字。
本次没有提交 Git commit；源码变更仍在工作树中。

### Neon 备份与回退

私有目录：`/home/xjc/jc_proxy-backups/neon-v3-20261001T043743Z/`

包含升级前 bootstrap、三个正式表的全部行、列/索引/约束定义、`rollback-data.sql.gz`、前后校验快照及迁移/测试日志。原文件旁另有 `migrate_neon.yaml.v2.20261001T044359Z.bak`。

数据库内额外保留：

- `jc_proxy_configs_v2_backup_20261001t043743z`
- `jc_proxy_upstream_keys_v2_backup_20261001t043743z`
- `jc_proxy_upstream_keys_meta_v2_backup_20261001t043743z`
- 配置表内升级工具生成的 `default.v2.backup.20261001T044359Z` 行

这些备份包含真实凭据，不要提交或分享；验证后也未自动删除它们。数据库内备份表保存的是数据快照，不复制原表索引和约束。

如需回退，先停止所有使用该数据库的实例。由操作人员检查后恢复三个表的数据、bootstrap 和兼容 v2 的应用构建；`rollback-data.sql.gz` 在事务中锁表并替换目标表全部数据，会丢弃备份之后的新写入，不能对运行中的服务直接使用。它依赖现存兼容表结构，不是完整数据库/DDL 恢复脚本；`recent_stats` 可暂时保留为空的额外列，不影响旧版按显式列名读取。迁移涉及文件、配置行、Key DDL 的多个提交点，不是跨资源原子事务。

## 保留边界

- 单进程控制面串行化不是多实例数据库 CAS；完整配置 PUT 仍是整体替换，不合并客户端持有的旧副本。
- 配置与 Key 分属不同持久化操作；批量 Key 操作、删除供应商与删除其 Key 分区仍不是跨资源事务。
- 已进入数据库的异步状态写仍依赖存储版本条件，未增加分布式事务或持久化意图日志。
- 累计统计的提交结果不明、多实例全局最近五次窗口、冷却状态跨进程重启恢复不在这次实现范围。
- Server 监听/TLS 等启动参数仍需重启，不因配置路由的安全发布而变成可热重载。
