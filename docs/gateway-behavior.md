# 网关链路行为边界

本文汇总数据面请求链路的当前行为与边界：临时响应、流式刷新、各级超时、错误预览/遮罩、
默认自动禁用、URL 处理、上传中断归因、结算与停机顺序。统计与 Key 策略见
[耗时统计与负载策略](performance-and-balancing.md)，控制面一致性见 [架构与升级](architecture.md)。

## 1. 重试安全不变量

以下属不可回退的投递语义，改动前应有测试钉死：

- **非幂等方法遇 5xx/网络错误绝不重放**（`POST` 只允许按配置重放 401/402/403/429）。
- 向下游写出第一个字节后，该响应永不重放。
- 单次使用的请求体（multipart / `Content-Length` 未知 / 超过缓冲上限）拒绝缓冲，而不是拒绝服务。
- balancer 对已排除 Key 严格排除，没有"实在没得选就用被排除的"兜底。
- 请求级全局尝试预算为单一计数（默认 10，配置上限 100），贯穿供应商故障转移与聚合 child 切换；已尝试 Key 集合跨聚合轮次共享，同一请求不会两次打到同一 Key。
- 成功响应（<400）不参与聚合重试；`aggregate.retry.status_codes` 配置 2xx 不会重发已成功请求。
- `failover.max_attempts` 校验范围为 1–20。

## 2. 临时响应与流式刷新

- `interim_response_interval` 开启时，网关在等待最终响应期间发送 HTTP `102 Processing` 保活，不写最终响应体。`WriteHeader(102)` 之后**不再单独 Flush**——`net/http` 中 Flush 会隐式提交最终 200，覆盖后续真实错误状态。最终 200/502/504 的状态与正文语义在 HTTP/1.1 与 HTTP/2 下一致。
- 普通流式响应在首块到达后同时提交头和正文并刷新一次，之后每个块立即刷新；不做 SSE 合并，不增加首 token 等待。合法空流仍刷新最终头。
- 刷新通过 `http.ResponseController` 优先读取 `FlushError`，支持 `Unwrap`；不支持刷新的 Writer 保持普通写出行为。
- `Write` 只写入服务端缓冲区可能返回成功，真正的网络错误在 `FlushError` 才出现：仅 Flush 失败记为下游中断；先前已确定的上游 HTTP/读取失败不被覆盖，只记录一次原始结果。

## 3. 响应读取与超时归因

- `body_timeout` 只约束正在执行的上游 `Read`。等待客户端 `Write/Flush` 不消耗该预算，慢下游不会导致健康 Key 被关闭、降权或记入 999 秒惩罚。成功响应的整体耗时统计仍包含下游背压，这与超时归因是两回事。
- 每个响应体复用一个延迟创建的 timer，记录当前读取 deadline，回调检查是否仍在读取；正常分块不逐块创建 timer/goroutine。EOF、显式 Close、真实读取超时保持独立；超时唤醒出的 EOF 仍是失败，客户端主动关闭不冒充上游超时。该机制增加约每读块 21ns 的同步成本。

## 4. 错误预览、遮罩与清理

- 同一份解码后的预览只生成一次文本摘要，同时供健康分类与遮罩匹配使用；规则顺序、Retry-After、自动禁用优先级和冷却延长语义不变。
- 预览仍是 **2048 字节或 EOF**，没有为错误响应增加更短的总超时：慢错误体仍可能阻塞到上游读取超时。遮罩和自定义文本规则保持原有匹配方式。
- 原始压缩字节用于透传，不用解码后的诊断文字替换上游正文；诊断解压上限 64KiB。
- 已在预览阶段读到 EOF 的遮罩响应无需再 drain；已知读取完整的错误体直接回放原始字节，不再读取已关闭的上游 body。
- 未读完且确定丢弃的遮罩错误体受总清理预算约束：**最多清理 64KiB / 50ms**，到期即 Close，不因涓流进度延长。这只用于错误清理，不进入成功响应或逐块热路径；超过预算放弃该上游连接的复用。清理不改变真实上游状态，不生成第二条失败或中断记录。
- 保留原始 HTTP 状态时，健康/计数仍保留原始失败；预览读取失败的供应商重试独立遵循 request-error 开关，并保留非幂等请求禁止重放的限制。
- 禁用临时响应路径返回 nil，收尾与提交方法兼容 nil（零分配路径）。

## 5. 默认自动禁用规则

- `incorrect_api_key` / `invalid_api_key` 是保留的结构化认证标识：只接受支持错误 envelope 的 `code`/`type`、顶层等价字段或精确字符串 `error`，**不匹配 message/model/param，不递归扫描用户数据**——上游把用户输入（如模型名 `invalid_api_key`）反射进错误文本时不再误禁用 Key。
- 非 JSON、截断 JSON 或只有 message 包含这些字样时，不依靠这两个关键词禁用；配置的 401 等状态码规则仍有效。其他自定义文本关键词、冷却和遮罩规则保持原有行为与优先级。
- **已有显式文本规则不会被自动改写**（包括旧表单写入的 `insufficient_quota` / `余额不足`）：若上游把用户输入反射到错误文本，这类规则仍有误触发风险。应检查既有配置，删除不可靠的全文禁用规则，或改用可信状态码/结构化认证标识。

## 6. 上传期限与中断归因

```yaml
upstream:
  upload_timeout: 300s          # 默认值；0s 关闭；负值拒绝
  response_header_timeout: 300s
  body_timeout: 5m
```

- `upload_timeout` 只用于非空请求体的发送阶段：覆盖连接获取/上传，成功写完请求或提前收到响应即停止，不限制后续响应头等待和长 SSE；**不包含请求体预缓冲**，也不是整个 HTTP 请求的总超时或上传无进展滑动窗口——发送很慢但持续进展的超大上传仍可能到期，应按业务调整。
- 实现使用请求 context 和 `httptrace.WroteRequest`，不设置 `http.Client.Timeout` 或上游连接 deadline。空 body 不安装 watchdog。
- 发送阶段到期且正阻塞在客户端 body 读取时保守归为中断，不处罚 Key；watchdog 到期同时使单次流式请求的下游读期限到期（`ResponseController.SetReadDeadline`）以唤醒读取：HTTP/2 只影响当前 stream，不修改共享连接 deadline；不预设或延长原有读超时，不对可重放请求设置此期限。
- 客户端读取原有的 `server.read_timeout` 保持有效；成功写完、提前收到响应或其他传输错误不修改它。未收到响应且确认为上游发送超时则按 request-error 处理，记一次失败/999 秒样本。
- 自定义 ResponseWriter 包装必须提供 `SetReadDeadline` 或 `Unwrap`；不支持时无法主动唤醒客户端读取，仍由服务器读超时兜底。
- 已发起 attempt 的客户端 body 读取错误（含真实 TCP 断开）计一次中断而非上游失败；错误来源包装穿透 Transport 层可识别，真正的上游 EOF 不会被泛化为客户端中断。客户端 context 尚有效时返回 400；watchdog 因读期限取消请求 context 时也尝试返回 400，避免 handler 直接结束隐式返回 200。
- 鉴权、预缓冲等尚未发起 attempt 的失败不记 Key 流量。
- YAML/管理配置 API 可修改该字段，热更新发布新值；供应商表单无专用输入框，但默认值、显式 0 和已有自定义值会保留。
- 成本：非空上传请求约增加 0.75µs、824B、10 次分配/attempt（context、trace、timer、响应体清理包装），无常驻逐请求 goroutine。

## 7. URL 处理

- 基础 URL 只接受 HTTP(S)、非空 host、无 fragment。
- 基础 URL 的查询参数在前、客户端原始查询在后，保留原始编码及重复参数；不承诺不同上游对同名参数取首值或末值的一致性。
- `%2F`、Unicode、百分号等保留线上的转义形式，不变成新的路由层级。基础路径以 `%2F` 结尾时也按转义形式统一拼接斜杠：`/base%2F` + `/files/a%2Fb` = `/base%2F/files/a%2Fb`。
- 路径改写匹配按 decoded path；精确改写显式替换整个路径，前缀改写保留未被替换的转义尾部。

## 8. 结算与停机

- `finish` 保持 exactly-once 语义：重试、masking、下游取消不重复计数；旧 Key 版本的在途结果保留累计流量，但不改变当前健康、最近错误及近期样本。
- `Pool.CompleteAttempt` 以 pool → stats 锁顺序原子提交样本、累计计数、版本和健康更新，reset/snapshot 不会拆开一个 outcome。该原子边界是单进程内一次结算，不是多实例全局事务。
- 重复 Append（重复/混合导入）不是恢复操作，不清除待持久化的禁用状态；过期更新仍由条件版本检查拒绝，存储临时失败恢复后 pending 不丢。
- <a id="停机"></a>停机顺序：优雅等待请求结束 → 到期后关闭剩余连接并等待全部 handler 完成结算 → 最终刷盘 → 关闭存储。`shutdown_timeout` 是优雅等待上限，不是无条件强退的整个进程退出上限；若第三方 handler/存储调用无视取消并永久阻塞，退出仍会等待。SIGKILL/崩溃、最终存储失败不能保证数据零丢失。

## 9. 已知限制与注意事项

- 每条流/错误清理增加了少量 timer 与同步成本（见 §3、§6）；错误路径分配相比早期实现减少约 30 次/请求，成功路径基本不变。
- `interim_response_interval` 的 102 保活与写入期限依赖 `http.ResponseController`；自定义 ResponseWriter 需支持 `Unwrap`。
- 自动禁用的历史误伤（旧宽泛文本规则禁用的 Key）不会自动恢复，需要管理员手动检查恢复。
- **管理面来源限制**：`admin.allowed_cidrs` 为空时 `AddrAllowed` 放行所有地址（fail-open），不配置就等于关闭网段限制；部署在反代后必须同时配置 `admin.trusted_proxy_cidrs`。
- **供应商测试接口的 SSRF 面**：vendor 测试接受请求体中的任意 `base_url`（仅校验 scheme 和 host 非空），并会挂上该 vendor 存储的上游 Key 请求该地址。该接口只允许已认证管理员调用，但管理员可以把 Key 发到任意主机（含内网），运维上应知悉并按需用反代手段限制。

复现链路微基准：

```sh
go test ./internal/gateway -run '^$' \
  -bench 'Benchmark(ResponsePipeline|UploadWatchdog)$' \
  -benchmem -cpu=1 -benchtime=300ms -count=3
```
