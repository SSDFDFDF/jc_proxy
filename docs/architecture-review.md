# jc_proxy 架构分析报告

> 日期：2026-08-20 · 分析基线：`main` @ `5bef164`
> 范围：`internal/gateway`、`internal/balancer`、`internal/keystore`、`internal/admin`、`internal/config`、`cmd/jc_proxy`
> 本报告只记录**已通过阅读代码或运行测试验证**的结论。未验证的推断已明确标注。

## 摘要

起点问题是「网关是否会把一个下游请求放大成多个上游请求」。答案是会，且实测最高 20 倍。
但沿着这条线往外查，发现放大只是表层症状：

| 层面 | 核心结论 | 严重度 |
|---|---|---|
| `gateway` 请求放大 | 两层重试预算相乘，实测 1 → 20 次上游请求 | 中（幅度） |
| `gateway` 结构 | 重试判定复制 3 份、回调穿透 3 层共 8 个调用点、状态作用域错一层 | 中（是上面 3 个缺陷的成因） |
| `keystore` 持久化 | per-key 状态有 3 种持久化等级且无统一策略；冷却/退避重启即丢 | 高（决定放大的**频率**） |
| `admin` 并发 | 配置读-改-写无锁，并发请求会丢更新 | 高 |
| 全系统 | 每条写路径都「先改活状态、后持久化、再丢弃失败」 | 高（根因） |
| 可观测性 | 完全缺失：0 指标、0 追踪、数据面仅 1 条日志、23 处 `_ =` 丢弃错误 | 高（前置条件） |
| 安全 | vendor test 可把存储的上游 key 发到任意主机；空 CIDR 白名单 fail-open | **需单独处理** |

**最重要的一条判断**：本轮开头那个 20 倍放大，在生产环境是**不可观测的**——必须手写探针测试才能发现。
因此任何优化的前置条件是先补观测，否则全是盲改。

---

## 一、起点：请求放大（internal/gateway）

### 1.1 成因：两层重试预算相乘

| 层级 | 代码位置 | 预算来源 | 默认 |
|---|---|---|---|
| 子供应商 key 故障转移 | `internal/gateway/router.go:308` 的 `for` 循环 | `error_policy.failover.max_attempts` | 5 |
| 聚合供应商换 child | `internal/gateway/router.go:247` 的 `for` 循环 | `aggregate.retry.max_attempts` | 2（校验上限 5） |

两个预算彼此独立，因此是乘法关系。实际上限 ≈
`Σ(被尝试的每个 child 的 min(failover.max_attempts, 该 child 的 key 数))`。

`error_policy.failover.max_attempts` **没有上限校验**：`config.go:1044` 只在 `<= 0` 时填默认值，
`validateErrorPolicy`(`config.go:1082`) 不检查它——而 `aggregate.retry.max_attempts` 在
`config.go:1130` 明确限制 1–5。两者不对称。

### 1.2 实测数据

探针方式见附录 A。聚合 2 个 child、每 child N 个 key、上游恒定返回错误码：

```
GET  429                                   -> 上游 6 次,  下游 429
GET  500                                   -> 上游 6 次,  下游 500
POST 429                                   -> 上游 6 次,  下游 429
POST 500                                   -> 上游 1 次,  下游 500   <- 安全
POST 401                                   -> 上游 3 次,  下游 401
POST 429 (no_default_backoff)              -> 上游 6 次,  下游 429

keys/child=5,  vendor_max=default, agg_max=default -> 上游 POST 10 次
keys/child=10, vendor_max=100,     agg_max=5       -> 上游 POST 20 次
```

即：一个客户端 POST 在持续 429 时最多打出 20 个上游 POST。

### 1.3 已有的安全保护（重构时必须保留）

这部分设计是对的，属于不可回退的不变量：

- `request_body.go:38` `canRetryResponse`：非幂等方法只允许 401/402/403/429 重放。
  **POST 遇 5xx 绝不重放**（实测 `POST 500 -> 1` 次），这是最重要的安全属性。
- `request_body.go:31` `canRetryRequestError` 要求 `safeRetry`，所以 POST 遇网络错误/超时不重放。
- `newSingleUseBodySource`(`request_body.go:128`)：multipart / `Content-Length` 未知 / >4MB
  的 body 拒绝缓冲而非拒绝服务。
- `response.go` `writeUpstreamResponse`：向下游写出第一个字节后永不重放。
- `balancer/pool.go:519` `isAvailableLocked` 对 excluded 严格排除，没有「实在没得选就用被排除的」兜底。

### 1.4 三个已确认缺陷

**缺陷 1：已试 key 集合不跨聚合轮次传递 → 同一 key 可能收到同一请求两次。**
`triedManagedKeyIdx` 在 `router.go:301` 每次进入 `serveVendorRequestWithAggregateHook` 时新建，
而聚合循环每轮都重新调用它。实测（两个 child 条目的 `key_ids` 有交集）：

```
child#0 key_ids=[k1,k2] / child#1 key_ids=[k2,k3]
上游收到的 key 顺序: [k1, k2, k3, k2]      <- k2 收到两次同一个 POST
```

触发条件：child 条目 `key_ids` 有交集，或冷却被关闭（`no_default_backoff: true`，
此时 429 走 `keyActionObserve` 不设冷却）。

**缺陷 2：2xx 成功响应可被丢弃并重发。**
`router.go:443` 在 `statusCode < 400` 分支里也调用了 `aggregateHook`；
`aggregateRetryable`(`response.go:229`) 的 `default` 分支查 `retry.status_codes`；
而 `validateStatusCodes`(`config.go:1160`) 只校验 100–599。
所以 `aggregate.retry.status_codes: [200]` 是合法配置，实测一个 GET 打出 2 次上游请求，
第一次成功的响应被丢弃。子供应商层没有这个洞（`router.go:370` 的 `>= 400` 闸门挡住了），
两层行为不一致。附带：`router.go:444` 会为被丢弃的那次记 `keyActionSuccess`，成功计数虚增。

**缺陷 3：`failover.max_attempts` 缺上限校验**（见 1.1）。

---

## 二、gateway 设计的结构成因

上面三个缺陷不是独立 bug，有共同成因。逐个对应：

| 症状 | 证据 | 导致 |
|---|---|---|
| 重试判定被复制 3 份 | `request_body.go` 的 `canRetryRequestError`(:31) / `canRetryResponse`(:38) / `canRetryAggregate`(:53) 近似重复；`{401,402,403,429}` 分支在 :46 和 :64 逐字出现两次 | 任一处漏改即缺陷 |
| 聚合重试用回调穿透 3 层调用栈 | `aggregateHook != nil` 在 `router.go` 出现 **8 次**（339/347/387/395/410/421/431/443），各处的 `applyDecision`/`Close` 顺序不一致 | **缺陷 2**——第 8 处落在 `< 400` 分支，唯独它没被 `>= 400` 闸门保护 |
| 状态作用域错一层 | `triedManagedKeyIdx` 属于「整个下游请求」，却建在内层循环（`router.go:301`）；两个 `max_attempts` 各属一层循环 | **缺陷 1** + 预算相乘 |

**结论：不要逐个打补丁。** 把「下游请求的一次投递」提升为一等对象：

- **候选流 + 单循环**：把「下一个该试谁」抽成迭代器（内部持有全局 tried 集合 + 优先级/权重策略），
  两层循环合成一层。tried 集合天然全局 → 缺陷 1 消失，并顺带覆盖「重复 child 条目」和
  「`key_ids` 交集」；8 个 hook 调用点塌缩成 1 个 `Evaluate` → 缺陷 2 在结构上不可能复发；
  `aggregateRetryHook`/`vendorRequestOutcome`/`upstreamResponseOutcome` 整套穿透式类型全部删除；
  非聚合供应商变成「只有一个 child 的聚合」，两条代码路径合并。
- **预算单位换成「上游请求数」**：现有两个旋钮描述的是「某层的循环次数」，
  既是相乘的原因，也都无法回答运维真正关心的问题——一个客户端请求最多花几次上游调用。
  改为入口处单一计数器贯穿全程，现有字段退化为推导默认值的输入。不要加第三个旋钮。
- **安全谓词换成「请求是否已被消费」**：`safeRetry = isSafeRetryMethod(method)` 加硬编码状态码白名单，
  把两个不同问题混在一起。真正要判断的是「上游有没有可能已经干活/计费/改状态」。
  建模为 `notConsumed`（401/403/402/429、连接未建立）vs `maybeConsumed`（5xx、响应头超时、读 body 中断），
  三个谓词合成一个。
- **幂等性应可声明**：现在 `POST /v1/messages/count_tokens`、`POST /v1/embeddings` 因为是 POST
  被当作不可重试，白白损失故障转移能力。加 per-vendor `idempotent_paths`，
  或认客户端的 `Idempotency-Key`（`headers.go` 白名单本来就放行该头）作为幂等证明。

### 2.1 一个被忽略的机会：把 N 次重复变成 0 次

gateway 里**没有任何 `time.Sleep` 或退避延迟**（已 grep 确认）。所以 429 的处理是：
把该 key 的 cooldown 记为 `Retry-After` 秒，然后**毫秒级内立刻打下一个 key**。
当 N 个 key 属于同一上游账号（最常见配置）时，一次请求会在 50ms 内烧穿全部 N 个 key，最后照样 429。

而恢复时间是已知的：`CooldownUntil`(`balancer/pool.go:37`)，`Stats()` 在 `:368-369` 已换算成秒暴露。
但投递路径完全没用它：

- `Retry-After` 在整个 gateway 里**只被读、从不被写**（`key_policy.go:73` 是唯一出现处）。
  gateway 自己合成的 503 `all vendor keys in cooldown`(`vendor_proxy.go:49`) 和
  502 `exceeded maximum failover attempts` 都走 `http.Error`，不带 `Retry-After`。
- 交付上游 429 时 `copyResponseHeaders`(`headers.go:81`) 会透传上游的 `Retry-After`，
  但那是**最后一个** key 的值，恰好最没参考价值。

**改法**：选候选前先看整个候选集是否全在 cooldown；若是，直接返回 429 +
`min(CooldownUntil)` 换算的 `Retry-After`，一次上游请求都不发。
客户端正确退避后，下一轮的重复也一并消失。
（注意：这会把原来的 503 变成 429，属于可观测行为变更，需确认下游客户端能接受。）

---

## 三、全系统：三种生命周期被塞进一个对象

系统里有三类东西生命周期完全不同，但现在全挂在 `Router` 上，每次重建一起销毁重造：

| 类别 | 应有生命周期 | 现状 |
|---|---|---|
| 路由计划（vendor→路由、header 规则、策略） | 不可变，随时可重建 | 在 `Router` |
| per-key 运行时状态（cooldown/backoff/failures/inflight） | 进程级，必须跨重载存活 | 在 `Router.pool` → **每次重建就死** |
| 基础设施（`http.Transport`/连接池） | 按 host 池化，长期复用 | 在 `Router` → **每次重建全丢** |

后果链条：

1. `Runtime.Update`(`runtime.go:54`) 每次都 `keySource.ListAll()` 重读全部 key、
   重建全部 vendor、重建全部 transport。
2. 因为状态被销毁，才需要 `MergeRuntimeStatsFrom`(`runtime.go:71` / `gateway.go:465`) 抢救回来。
   `runtime.go:65-69` 的注释本身就是这个设计缺陷的自述。
3. `admin/service.go` 有 **6 处** `RefreshKeys()`（:340/:370/:400/:415/:451/:478）。
   即**每次增删启停 key，都会丢光所有上游的 keep-alive 连接**，下一个请求对每个 vendor
   重付 TCP+TLS。而且旧 transport **从不 `CloseIdleConnections()`**
   （`MergeRuntimeStatsFrom` 只碰 `pool`），泄漏到 `IdleConnTimeout`(90s) 才回收。
4. `buildVendorGateway`(`gateway.go:543`) 是全文件唯一的 `http.Transport` 字面量，
   在每 vendor 每次重建时执行。多个 vendor 指向同一 host 就是多套连接池，各自 `MaxIdleConnsPerHost: 128`。

**唯一做对的地方**：`runtimeStatsRegistry` 挂在 `Runtime` 上而非 `Router` 上，按 `(vendorID, key)` 索引，
是唯一正确跨重载存活的状态。它已经证明了正确模式，只是没推广到其余状态。

### 3.1 配置的热/冷字段没有边界

`cfg.Server.*`（Listen / 各 timeout / TLS）只在 `main.go:96-110` 启动时读一次，之后无人再读（已确认）。
但 `Service.UpdateConfig` 接收整个 config，只把 `Storage` 从旧值钉回（`service.go:113`）。
所以从 admin 改 `server.listen` 或 TLS：**校验通过、router 重建、写盘成功、运行时毫无变化**。
静默无效是最差的结果——要么标记为运行时不可变并明确拒绝，要么让 listener 可重载。
`admin.session_ttl`、`admin.audit_log_path` 同样在 `main.go:87-88` 启动时捕获。

另外 `PrepareAndValidate` 把默认值写进同一个会被序列化回 `config.yaml` 的结构体，
默认值会在第一次 admin 保存时物化进用户文件，造成配置漂移。
拆成 `FileConfig`（用户写的）/ `EffectiveConfig`（应用默认后的）可消除。

### 3.2 观测层完全缺失

已 grep 确认：没有 prometheus / otel / slog / zap / logrus。整个数据面只有 **1 条**
`log.Printf`（`stats_persister.go:48`，且是关于 stats 持久化的），`internal/gateway`
里有 **23 处** `_ =` 丢弃错误。

也就是说：key 被自动禁用、发生故障转移、429 风暴、禁用状态持久化失败——**全部静默**。

---

## 四、持久化层（internal/keystore）

### 4.1 三种持久化等级，无统一策略

`keystore.RuntimeStats`(`store.go:36-45`) 只有 8 个计数器字段，
**没有 `CooldownUntil`、没有 `CooldownLevel`、没有 `Failures`**：

| 状态 | 跨 router 重建 | 跨进程重启 |
|---|---|---|
| 计数器（TotalRequests/SuccessCount/…） | 存活（registry 挂在 `Runtime`） | **存活**（5s delta 落盘） |
| `CooldownUntil` / `CooldownLevel` / `Failures` | 存活（靠 `MergeRuntimeStatsFrom` 抢救） | **丢失** |
| `Status = disabled_auto` | 存活 | **可能静默丢失**（见 4.2） |

**运维上最重要的两个事实恰好最不持久。** 重启一次：所有冷却清零
（30 分钟的 401 冷却、3 小时的 `payment_required` 冷却全丢），退避等级归零 →
网关按第一章的行为向全部 key 扇出 → 再次全部失败 →
花掉 N 次上游请求和 N 次配额惩罚，只为重新学到重启前已知的事。

**第一章的放大问题与本节的持久化缺口是乘法关系**：前者决定放大的**幅度**，后者决定**频率**。

### 4.2 「这个 key 已经死了」沿途被丢弃 5 次

| # | 位置 | 行为 |
|---|---|---|
| 1 | `gateway/vendor_proxy.go:172` | `pool.Disable(...)` 内存生效 |
| 2 | `gateway/vendor_proxy.go:185` | `_ = asyncStore.SetStatusIfVersion(...)` 返回值丢弃 |
| 3 | `keystore/async_store.go:181-199` | 立即返回 `nil`，调用方永远无法得知是否落盘 |
| 4 | `keystore/async_store.go:295` / `:315` | `ErrVersionMismatch` 与 `err == nil` 落在**同一 case 分支**，条目被删除，无日志无计数 |
| 5 | `cmd/jc_proxy/main.go:71` | `defer keyStore.Close()`；`flushPendingFinal` 每条只尝试一次，失败即丢，错误被 defer 丢弃 |

pending 是纯内存 map、从不落日志，所以任何 `log.Fatalf`、SIGKILL 或崩溃丢掉全部未同步意图。

第 4 点的语义本身可辩护（版本号变了说明别人已改写，陈旧的自动禁用不该覆盖），
但**它不可观测**：内存池认为 key 已禁用、存储认为仍启用，两边静默分叉。
可观测症状是「重启后已知坏 key 复活、拿到流量、再次失败」。

### 4.3 其他结构问题

- **`AsyncStatusStore` 是合并 map 而非队列**：`pending map[string]pendingStatusUpdate`
  按 `vendorID\x00key` 索引（`async_store.go:46`），同 key 后写覆盖前写。
  无上限、无背压、无溢出；内存随「未同步的 (vendor,key) 对数」增长，实际由 key 总数封顶。
  失败重试固定 250ms、无上限、无退避（`async_store.go:13`）。
- **file 驱动每 5 秒全量重写 JSON**：`RuntimeStatsPersister` 默认 5s ticker
  （`stats_persister.go:13`，`main.go:81` 传空 options 所以生产就是 5s），
  `FileStore.ApplyRuntimeStatsDeltas`(`file_store.go:254-305`) 在内存副本累加后**整文件重写**。
  写入量与 key 总数成正比，与实际变化量无关。
- **两条写路径共用一把锁和一个 substrate**：状态写走合并 map、统计写走 5s delta，
  但 file 驱动下共享 `FileStore.mu` 和同一次全量重写。且 `ApplyRuntimeStatsDeltas`
  在 async 包装层是**同步直通**（`async_store.go:209-214`），统计刷盘会阻塞 persister goroutine。
- **明文 key 进错误信息并打日志**：`async_store.go:299` / `:318` 用
  `fmt.Errorf("vendor=%s key=%s: %w", ..., update.key, ...)` 拼入明文 key，
  默认 error handler 打印它（`:70`）。代码库其他所有地方都刻意用 `KeyID` 或掩码
  （`balancer/pool.go:376`、`admin/service.go:509`）——**这一处违反项目自己的约定**。
- `KeyID` = `sha256(TrimSpace(key))` 取前 16 字节 hex（`store.go:121-128`），
  无盐、确定性，因此跨重启与跨后端稳定，可作为配置级引用（`gateway.go:385`）。
  对高熵 API key 而言是指纹而非保密手段，这个取舍合理。

---

## 五、控制面（internal/admin）

### 5.1 并发丢更新（已确认）

`Service` 结构体**没有 mutex**（`service.go:18-24`）。存在两把锁，但都不覆盖临界区：

- `Runtime.updateMu`(`runtime.go:24`) 只覆盖 `runtime.go:59-75`（重建 + 交换指针）。
- `Store.mu`(`admin/store.go:38`) 只保护内存里的 `s.cfg` 指针；
  `UpdateConfig` 在**锁外**写远端后端和文件（`store.go:163-172`），仅在交换指针时加写锁（:174-176）。

每个 mutator 都是 `GetConfig` → 改克隆 → `UpdateConfig`，中间不持任何锁：
`CreateVendor`(:182)、`UpdateVendor`(:208)、`RenameVendor`(:238)、`DeleteVendor`(:313)、
`RotatePassword`(:130)、`AddClientKey`(:600)、`DeleteClientKey`(:627)、`UpdateConfig`(:104)。

后果：

- **两个并发 `CreateVendor` 会丢掉一个**——从 config.yaml 和 router 里同时消失。
  重名/重复 key 检查（:191/:259/:612）因此全是 TOCTOU。
- **固定临时路径 `path + ".tmp"`**(`admin/store.go:261-267`)：rename 本身原子，
  但并发写者争用同一个临时文件，A 可能把 B 的字节 rename 上去。全仓库无 flock / `O_EXCL`。
- **顺序反转**：`updateMu` 在文件写之前就释放，所以 router 里可以是请求 B 的配置、
  而 config.yaml 里是请求 A 的。
- `internal/admin` 里**没有任何并发测试**（无 `t.Parallel`、无并发变更测试）。

### 5.2 一致性缺口

- **`DeleteVendor`**(`service.go:313-345`)：写配置+重建(:334) → `keyStore.DeleteVendor`(:337) →
  `RefreshKeys`(:340)。若 :337 失败，vendor 已从配置消失而 key 分区留成孤儿，无回滚。
  **但这是已知并显式暴露的**——`ListUpstreamKeys` 用 `Configured:false` 标出孤儿
  （`service.go:529-533`、`:583-594`），属于有意识的取舍。
- **批量操作部分生效**：`SetUpstreamKeyStatus`(:473-477)、`RecoverUpstreamKeys`(:446-450)
  在首个错误处返回，前面的 key 已生效，且整条批量审计记录被跳过。
- **`SetUpstreamKeyRemark`**(:489-511) 唯一不调用 `RefreshKeys` 的 keystore mutator，
  备注对活 router 不可见，直到某个无关变更触发重建。
- **审计写在变更之后**（如 :343/:373）且失败被静默吞掉（`audit.go:244-246`），中间崩溃即丢记录。
- **无文件监听 / SIGHUP 重载**：配置只能经 admin 写入或重启生效。

### 5.3 安全（本轮不处理，仅记录）

**vendor test 可把存储的上游 key 发到任意主机。** `resolveVendorTestBaseURL`
(`gateway/vendor_testing.go:278-295`) 接受请求体里的任意 `base_url`，
**只校验 scheme 和 host 非空**——无主机白名单、无内网地址拦截。
而 `buildTestHeaders`(:262-266) 无条件挂上该 vendor 存储的上游 key，
`{{upstream_key}}` 还会替换进注入头(:258)。

严重度被第二个问题放大：`AddrAllowed` 在**前缀列表为空时返回 `true`**
(`config.go:1282-1284`，已确认)，而 `admin.allowed_cidrs` **从无默认值**——
只有 env 覆盖(:437) 和格式校验(:874)。**不写 `allowed_cidrs` 就等于关闭网段限制，fail-open。**

其他：`service.go:45-48` 有明文密码回退分支；console 静态资源由 gateway 提供
(`router.go:97-112`)，**只过 CIDR、不校验 token**；admin API 以明文返回 key
(`models.go:70` ← `service.go:571`)，`masked` 字段是装饰性的；
`migrate_neon.yaml` 含看起来有效的 Neon 明文密码（已 gitignore，但明文在磁盘上）。

会话鉴权本体是可靠的（见附录 B），所以以上不构成未认证利用，但对合法管理员是 SSRF + 凭据外泄陷阱。

**建议修复**（三处都很小）：vendor test 的 `base_url` 加主机白名单或限制为该 vendor 已配置的 host；
拦截私有地址段；空 `allowed_cidrs` 改 fail-closed 或至少启动告警。

---

## 六、统一诊断：一个缺陷，在五个地方各写了一遍

把三个子系统的发现并排放：

| 位置 | 顺序 | 失败后 |
|---|---|---|
| gateway 自动禁用 key | 内存生效 → 异步落盘 | 返回值 `_ =` 丢弃（`vendor_proxy.go:185`） |
| `AsyncStatusStore` | 立即返回 nil | 版本冲突与成功同一分支，静默删除（`async_store.go:295/315`） |
| admin `UpdateConfig` | `runtime.Update` → `store.UpdateConfig` | 无回滚，进程跑新配置、磁盘存旧配置（`service.go:117-120`） |
| `DeleteVendor` | 改配置+重建 → 删 key 分区 | 无回滚，留孤儿分区（`service.go:334-337`） |
| 批量 key 操作 | 逐个应用 | 首个错误即返回，前面已生效，审计整条跳过（`service.go:446-450/473-477`） |

> **全系统每一条写路径都是「先改活状态、后持久化、再丢弃持久化失败」。
> 没有一处 write-ahead，没有一处回滚，也没有一处能观测到这种分叉。**

这一条解释了第四章的两个现象——持久化等级为什么互不一致、
「key 已死」这条信息为什么沿途被丢弃 5 次。它们不是两个问题，是同一缺陷的两个投影。

---

## 七、目标架构

分层只是手段，真正要立的是三条不变量：

**不变量 1：持久化先行，活状态后改（write-ahead + 单一提交点）。**
所有变更走一条路径：写意图日志 → 持久化 → 应用到活状态 → 确认。
失败发生在应用之前，因此不需要回滚。这一条同时消掉第六章表格的五行。
落地上是一个 `applier`：admin 和 gateway 的自动禁用都经过它，而不是各自直连存储。

**不变量 2：控制面变更串行化。**
`Service` 加一把 mutex 覆盖整个 read-modify-write（单进程够用）；
临时文件改成 `O_EXCL` 随机名或 flock。多副本部署需在存储层做 CAS
（pgsql 后端已有 version 机制，可复用到 config 行）。修掉 5.1 全部问题。

**不变量 3：每个被丢弃的失败都要有一个计数器。**
不是「加点指标」，而是**约定层面禁止静默丢弃**。
最小指标集（Go 1.26 标准库 `log/slog` + 原子计数器即可，无需新依赖）：

| 指标 | 回答什么问题 |
|---|---|
| histogram `upstream_attempts_per_downstream_request` | 第一章的放大，一个指标直接暴露 |
| counter `retry_total{scope=key\|child, reason=429\|5xx\|network}` | 放大的来源构成 |
| counter `key_auto_disabled_total{vendor,reason}` | 自动禁用发生率 |
| counter `key_status_persist_failed_total` | 4.2 的第 2/3/5 点 |
| counter `key_status_version_conflict_total` | 4.2 的第 4 点（从「当成功」改为「计数+日志再删」） |
| gauge `keys_available{vendor}` | 503 `all keys in cooldown` 的归因 |

在三条不变量之上，分层如下：

- **L1 `plan` 不可变**：编译产物（vendor→候选列表、header 规则、rewrite、策略），
  纯数据，不持有 live 状态和 `http.Client`，原子替换。这才是 `Router` 应该是的东西。
- **L2 `state` 挂 `Runtime`、按稳定 ID 索引**：把 `runtimeStatsRegistry` 的模式推广到
  全部 per-key 状态（含 `CooldownUntil`/`CooldownLevel`/`Failures`），键 `(vendorID, keyID)`；
  再加一层按 `host` 索引的熔断状态。**删掉 `MergeRuntimeStatsFrom`**
  （它是导出给「没有人」的，唯一调用者是同包的 `runtime.go:71`）。
  重载从「状态毁灭事件」变成一次指针交换，6 处 `RefreshKeys` 自然变廉价。
- **L3 `infra` transport 按 host 池化**：跨重载复用，并正确 `CloseIdleConnections()`。
  同时让聚合的多个 child 指向同一 `base_url` 时能共享连接池与熔断状态。
- **L4 观测**：见不变量 3。
- **L5 控制面边界反转**：admin 对 gateway 的依赖面只有 9 个符号，其中
  `Update`/`RefreshKeys`/`RecoverUpstreamKey`/`VendorStats` 和**整个 `vendor_testing.go`**
  都只为 admin 存在。正确切法是把 `vendor_testing.go` 整体移到控制面侧
  （顺带让 5.3 的主机白名单校验落在控制面而非数据面包里），
  gateway 只暴露 `Apply(plan)` / `Snapshot()` / `Recover(key)` 的接口。

---

## 八、优先级与落地顺序

| # | 内容 | 风险 | 说明 |
|---|---|---|---|
| 0 | 5.3 的安全三项 | 低 | 与其余全部无关，应独立尽快做掉 |
| 1 | 不变量 3（观测）+ 不变量 2（串行化） | 零行为风险 | **后面一切的前提**；两者不冲突，可同批完成 |
| 2 | 不变量 1（write-ahead），含冷却/退避纳入持久化 | 中 | 压低放大的**频率** |
| 3 | 1.4 三处小修 + 全局 `attemptTracker` + 塌缩 8 个 hook 调用点 | 低 | 压低放大的**幅度**；1.4 三处是两行级 |
| 4 | L2 状态提升 + 删 `MergeRuntimeStatsFrom`；L3 transport 池化 | 中 | |
| 5 | L5 接口反转 + `vendor_testing.go` 迁移；L1 plan 拆分 + 候选迭代器 | 高 | 见下方风险说明 |
| 6 | 配置冷热分离 / `FileConfig` 拆分；file 驱动全量重写改增量 | 中 | 可独立进行 |

**顺序理由**：#1 之前的任何优化都是盲改（第一章的放大问题在生产不可观测）。
#2 排在 #3 之前，因为 #2 修的是放大的频率、#3 修的是幅度，前者收益更大。

**唯一有真实风险的是 #5 的候选迭代器**：拍平后权重与优先级分层的交互语义会变。
现在 `aggregatePool.pickAvailableLocked` 是「先取最高优先级层、再在层内按权重选」
（`gateway.go:270-321`），扁平迭代器需显式保持这个两级语义，否则权重会跨层泄漏。
重构前应先为 1.3 的每条不变量补上钉死测试，尤其 **POST 遇 5xx 不重放**。

---

## 附录 A：验证方法

- 逐文件阅读 `internal/gateway`（13 个非测试文件 3424 行）全部投递路径、
  `internal/balancer/pool.go` 的选择与排除逻辑、`internal/config` 的默认值与校验、
  `cmd/jc_proxy/main.go` 的装配与生命周期。
- `internal/keystore`、`internal/admin` 由子代理按定向问题清单核查，结论均带 `file:line`。
- **放大倍数为实测**：在 `internal/gateway` 内临时写探针测试
  （`httptest` 上游 + `atomic` 计数 + `newTestRouter`/`mutateTestVendor` 既有 helper），
  覆盖 `{GET,POST} × {401,429,500}`、`no_default_backoff`、`retry.status_codes:[200]`、
  child `key_ids` 交集、以及 keys/child × max_attempts 的组合。
  探针文件已删除，工作树干净；复现只需按上述参数重建。
- 全仓库 grep 确认的否定结论：无 prometheus/otel/slog/zap/logrus、
  无 `time.Sleep` 退避、无 fsnotify/SIGHUP 重载、无 flock/`O_EXCL`、
  `internal/admin` 无并发测试、无 sqlite 后端。
- `go build ./...` 与 `go test ./internal/gateway/` 在分析期间均通过。

**未验证的部分**：`internal/admin` 与 `internal/keystore` 的结论来自子代理阅读，
我未逐行复核其全部引用（对严重度最高的两条——空 CIDR fail-open 与
vendor test 的 `base_url` 校验——已亲自复核代码确认）。
pgsql 后端的行为未做运行时验证，仅静态阅读。多副本部署下的并发行为未验证。

## 附录 B：应保留的设计

重构时不要动这些：

- 1.3 列出的全部重试安全保护，尤其 **POST 遇 5xx 绝不重放**。
- `balancer/pool.go:519` 对 excluded 的严格排除，无「实在没得选就兜底」。
- admin 的 `SetStatus` 是**同步持久**的，只有 gateway 的 `SetStatusIfVersion` 走异步
  （`async_store.go:165-199`）——这个快慢分离是正确的。
- `DeleteVendor` 的孤儿分区**已知并显式暴露**（`Configured:false`），是取舍不是漏洞。
- 鉴权本体：PBKDF2-SHA256 12 万轮 + 常量时间比较（`password.go:277-320`）、
  按 IP 指数锁定（`login_guard.go`）、`X-Admin-User` 服务端覆写不可伪造（`handler.go:80`）、
  改密码即全量失效会话（`service.go:123-125`）。
- 两个 store 都拒绝就地迁移、版本不匹配拒绝启动，迁移交给独立的 `jc_proxy_upgrade`。
- `main.go` 的 defer LIFO 顺序（stats 先刷、keystore 后关）是对的。
- `keystore` 的 `Replace` 有手写的 pending 重对账（`async_store.go:343-428`），
  不是简单丢弃，这块比其他路径严谨。
- 供应商 ID 化（schema v2）：key 分区、聚合 child、运行时统计全部按不可变 `vendorID` 索引，
  重命名不会破坏拓扑。这是本次分析中受益最明显的既有设计。

## 附录 C：本报告的自我修正

分析过程中我提出过两个后被证伪的判断，记录以免误导：

1. **「`keystore -> config` 是分层反转，存储层不该认识文件 schema」——撤回。**
   实际是两个耦合：构造函数用 `config.UpstreamKeyStoreConfig` 当 DI 参数（可反转，但无害），
   以及共享 `config.CurrentSchemaVersion`/`LegacySchemaVersion`（`schema.go:9-26`）——
   **后者是刻意设计**，一个版本常量同时约束 config 文件、config 表、key 文件、key 表。
   `internal/config` 不 import 任何 internal 包，无环。这条依赖合理。
2. **「vendor 名可能与 `/healthz`、`/console` 路由冲突」——不成立。**
   `config/vendors.go:20-21` 已有保留名集合。
