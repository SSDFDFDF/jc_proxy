# 供应商请求插件

插件在客户端鉴权、供应商选择和路径重写之后，选 Key 和发送请求之前运行。首选 Go `RequestPlugin` 接口；JavaScript 通过同一接口作为可选适配器。历史过滤同时提供原生实现和脚本示例。

只有原始供应商可以配置 `plugins`。聚合供应商配置非空插件列表会被拒绝（即使插件禁用）；聚合转发时执行选中子供应商自己的插件。

## 配置

在供应商配置下添加：

```yaml
plugins:
  - name: history-filter
    builtin: codex-history-filter
    enabled: true
    methods: [POST]
    paths: [/v1/responses, /responses]
    timeout: 100ms
    max_body_bytes: 4194304
```

管理后台的原始供应商详情提供「请求插件」编辑器，可启停、排序和选择原生/脚本实现。YAML、原始 JSON 配置、供应商配置 API 同样支持。更新单个供应商时省略 `plugins` 会保留已有插件，显式 `[]` 清空；完整配置替换仍按完整替换语义处理。

| 字段 | 语义 |
| --- | --- |
| `name` | 供应商内唯一名称，必填，最多 64 字节 |
| `builtin` | 编译进二进制的原生插件，目前支持 `codex-history-filter` |
| `script` | 本地 JavaScript 文件，相对**进程工作目录**或绝对路径 |
| `source` | 内联 JavaScript，可随配置存入文件或 PostgreSQL |
| `enabled` | 默认 `true`；`false` 不加载、不执行 |
| `methods` | 大写 HTTP 方法列表，空列表匹配全部 |
| `paths` | 路径重写后、拼接上游 base path 前的 decoded path；精确匹配或末尾 `*` 前缀匹配，空列表匹配全部 |
| `timeout` | 单个插件执行预算，默认/0 为 100ms，上限 5s；JSON API 中为纳秒 |
| `max_body_bytes` | 单个插件输入和输出字节数上限，默认/0 为 4 MiB，上限 64 MiB |

`builtin`、`script`、`source` 必须三选一。每个供应商最多 16 个插件，按配置顺序执行，后一个读取前一个的输出。应为历史过滤配置明确的 Responses 路径，避免作用于其他 JSON API。

脚本最多 1 MiB，在构建候选路由时读取、编译并验证 `onRequest`，不逐请求读文件或编译。失败会阻止持久化和路由发布，现有路由继续工作。修改脚本文件后需触发配置保存、路由重建或重启；没有文件监听器。Key 刷新也会重建路由并重新读取脚本。已有请求始终持有旧的已编译脚本。

## Go 接口

接口定义在 `internal/plugins/plugin.go`，原生历史过滤位于 `internal/plugins/codexhistory/`，不依赖 gateway 包。网关只负责注册、加载、调用和统计：

```go
type RequestPlugin interface {
    Name() string
    Matches(method, path string) bool
    BodyLimit() int64
    Transform(context.Context, plugins.Request) (plugins.Result, error)
}
```

`plugins.Request` 包含只读 `Body []byte`、`Method`、`Path`、`VendorID`、`Vendor` 和 `Provider`。实现必须支持并发、遵守上下文取消、不得修改输入切片；不变时可以返回 `plugins.Result{Body: req.Body}`。`Result.Summary` 可选，在转换过程中累计数字，供管理页显示，不需要再次解析正文。原生实现可复用 `plugins.Matcher`，在 `internal/gateway/plugins.go` 的 `builtinRequestPlugins` 注册工厂，重新编译后通过 `builtin` 选择。工厂只在路由构建时运行，请求间共享不可变实例。Go 插件编译进主程序；需要独立部署、修改文件后重新加载的扩展使用 JavaScript。

当前扩展点只转换请求体，不提供上游凭据、网络调用、响应/SSE 改写或重试控制。原生业务拒绝返回 `*plugins.RejectError`，执行错误返回普通 `error`。

## JavaScript 接口

改用示例脚本只需将 `builtin` 替换为：

```yaml
script: ./plugins/codex-history-filter.js
```

也可以使用内联脚本，例如按供应商修改模型名：

```yaml
plugins:
  - name: model-alias
    methods: [POST]
    paths: [/v1/responses]
    source: |
      function onRequest(request) {
        let body;
        try { body = JSON.parse(request.body); }
        catch (_) { reject(400, "Invalid JSON request."); }
        if (body.model === "client-model") body.model = "upstream-model";
        return JSON.stringify(body);
      }
```

`request.body` 是 UTF-8 字符串，其余字段为 `method`、`path`、`vendor_id`、`vendor`、`provider`。返回字符串替换 body，返回 `null`/`undefined` 保持当前原文。`reject(4xx, message)` 立即拒绝请求，消息长度为 1–512 字节。必须使用同步函数；Promise、对象等返回值会被视为执行失败。

历史过滤脚本可选调用 `reportHistory(reasoningRemoved, idsRemoved, includesRemoved, storeDisabled)` 上报本次摘要：前三个参数为 0–67108864 的整数，最后一个为布尔值。多次调用保留最后一次；仅成功返回且输出大小合法时记录。不调用也会统计执行结果、耗时与字节数。此 API 不接受自定义文本、请求内容或 ID。示例脚本已经包含上报逻辑。

引擎使用 Goja，不需要 Node.js，不提供 `require`、文件、网络、进程或定时器 API。每次调用创建独立运行时，不共享脚本全局变量；程序预编译。循环执行受到超时/客户端取消中断，调用栈限制为 256。它没有独立进程的内存隔离，脚本只应由受信任的管理员配置；body 上限限制输入和输出，不是 JavaScript 堆内存配额。原生代码和引擎内部操作的取消在检查点生效，不是操作系统级抢占。

脚本自身使用 `JSON.parse`/`JSON.stringify` 时遵循 JavaScript 数值语义；需要精确保留大整数的历史过滤优先选择原生实现。

## 历史过滤行为

原生 `codex-history-filter` 与示例脚本实现主动过滤：

- 删除 `input` 中的 `type: reasoning` 历史项。
- 仅删除 message、function_call、function_call_output、custom_tool_call、custom_tool_call_output 及省略 type 的消息项上的顶层 `id`。
- 保留工具配对 `call_id`、正文、图片、嵌套资源 ID、未知工具类型 ID 和当前请求的 `reasoning` 参数。
- 设置 `store: false`，从 `include` 移除 `reasoning.encrypted_content`。
- 对 `previous_response_id` / `conversation` 引用、compaction、item_reference 和非 reasoning 项的加密上下文返回 400；这些上下文不能通过删 ID 无损迁移。

规则参考 [AgentRouter-Codex-Filter](https://github.com/alexcarterio/AgentRouter-Codex-Filter) 的 strip 模式，MIT 许可见 [许可证](../plugins/LICENSE.AgentRouter-Codex-Filter)。不包含其懒降级、代理服务或重试机制。需要不同规则时可修改脚本或增加原生实现。

## 与现有网关行为的关系

- 未配置插件的供应商直接跳过插件链；方法/路径不匹配时不读取或缓冲 body。
- 命中后才需要完整 UTF-8 body；已有重放缓冲会被复用，未知长度请求有界读取。超过输入限制返回 413，非 UTF-8 返回 400，非 identity 的 `Content-Encoding` 返回 415，不会把未处理的压缩内容直接转发。
- 单个子供应商的一轮派发只处理一次；切 Key 重试复用处理后的 body。聚合切换子供应商重新从原始 body 开始，各子供应商互不污染。
- body 重放能力不放宽现有非幂等方法重试限制；上游错误分类、Key 健康、冷却、总尝试预算及聚合策略全部沿用原逻辑。
- 插件在选 Key 前拒绝/失败，不计入 Key 请求与健康状态，也不因本地脚本失败自动切供应商。执行失败返回 500 且不回显异常正文；4xx 拒绝和 500 都经过现有错误屏蔽规则，聚合入口仍控制最终错误展示。
- body 改变后重新计算 Content-Length，清除旧 Content-Encoding、Content-MD5、Digest、Content-Digest、Repr-Digest。响应和 SSE 保持现有流式转发。
- 管理后台供应商测试也会应用插件。插件执行预算不包含读取客户端上传的时间，上传预读继续受服务端 `read_timeout` 约束。

## 查看插件运行情况

在管理后台「供应商 → 原始供应商 → 请求插件」查看当前**已保存配置**的运行状态，点击「刷新状态」获取最新数据。该按钮复用现有 `/admin/stats` 请求，不新增定时轮询，也不会覆盖正在编辑的配置草稿。供应商测试使用同一插件链，可发起匹配方法/路径的请求后刷新验证。

- 已加载但没有记录：插件已初始化，尚无实际执行。检查匹配方法/路径、客户端鉴权和前序插件是否拦截。
- 执行数 = 改写 + 未改写 + 主动拒绝 + 执行失败；改写以输入/输出字节是否一致为准，不代表上游请求成功。
- 执行前拦截单独计数，包括请求体过大、读取失败、非 UTF-8、压缩编码不支持。预读错误归到第一个命中的插件；链中输入超限归到对应插件，后续未执行的插件不计数。
- 最近摘要包含完成时间、插件自身执行耗时（不含上传/上游耗时）、输入/成功输出字节数，以及历史 reasoning、顶层 ID、include 的删除数量和是否将 store 改为 false。拒绝或失败只记录固定原因码，不记录异常正文，也不会展示未送出的部分处理摘要。
- 每个已加载插件只保存固定大小的内存计数和一条最近摘要，不保留正文、ID、Key、请求列表；不写日志、磁盘或数据库。插件计数不受 Key 统计重置影响。
- 统计按原始供应商 ID 归属；聚合流量记在实际子供应商，切 Key 重试不重复执行或计数。Key 刷新/无关配置保存保留计数；修改插件配置、名称或脚本内容后重置，禁用/删除后移除，进程重启清零。

已有鉴权保护的 `GET /admin/stats` 增加 `plugins: {供应商ID: [...]}`，只包含启用并成功加载的插件。`vendor_id` 同时过滤插件统计，Key 的搜索和分页不裁剪插件列表。无需升级配置数据库。

## 性能验证

运行：

```sh
go test ./internal/gateway -run '^$' -bench '^Benchmark(RequestPlugins|PluginObservation)$' -benchmem -cpu=1 -benchtime=300ms -count=3
```

基准比较无插件分支、不匹配路径、原生历史过滤和 JavaScript 示例，覆盖 4 KiB 与 128 KiB 请求体。它使用已缓冲请求，测量转换和脚本运行成本，不包含网络、首次上传预读或上游耗时；无插件分支的亚纳秒数字不能解释为网关请求延迟。

本机 Go 1.26.1 / Linux amd64 / i9-14900HX，包含运行统计，`-cpu=1 -benchtime=300ms -count=3` 的中位数（请求大小为近似值）：

| 场景 | 耗时/次 | 分配字节/次 | 分配次数/次 |
| --- | ---: | ---: | ---: |
| 已配置但路径不匹配 | 13 ns | 0 | 0 |
| 原生历史过滤，4 KiB | 51 µs | 32,369 B | 77 |
| JavaScript 历史过滤，4 KiB | 88 µs | 78,600 B | 453 |
| 原生历史过滤，128 KiB | 1.44 ms | 838,818 B | 77 |
| JavaScript 历史过滤，128 KiB | 1.60 ms | 1,624,584 B | 463 |

`BenchmarkPluginObservation` 用直接返回输入的插件隔离观测成本：直接调用约 11 ns，记录计数/时间/最近摘要约 106 ns，增加约 **95 ns（0.095 µs），0 B / 0 次堆分配**。另以 `-cpu=32` 测试同一插件的并发计数约 184 ns/次，仍为 0 次分配；这是计数热点的吞吐基准，不是生产请求延迟。

相较添加观测前，原生 4 KiB 的 32,369 B / 77 次分配保持不变；JS 示例增加约 792 B / 10 次分配，用于可选摘要 API 和数字计数。数字在已有过滤循环中累计，不重复解析正文；单插件还复用了原有 body 比较结果。耗时有 CPU 频率与 GC 波动，不能将微基准差异理解为性能提升。追求最低开销优先使用原生实现。

另用已有 `BenchmarkRouter_StubTransport` 对比改动前后的无插件完整转发链：JSON 均为 3,976 B / 44 次分配，SSE 均为 3,920 B / 43 次分配；耗时落在约 4–5 µs 的测量波动范围，未观察到新增分配。以上为本机微基准，不代表生产环境吞吐保证。
