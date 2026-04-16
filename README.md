# jc_proxy

一个面向多供应商 LLM API 的轻量代理服务，支持按供应商路由、上游 Key 轮询与熔断、管理后台和配置热更新。

## 功能概览

- 按路径转发到不同上游，例如 `/openai/...`、`/anthropic/...`
- 支持 `round_robin`、`random`、`least_used` 三种 Key 选择策略
- 内置错误分类、自动 cooldown、自动禁用失效 Key、故障切换
- 支持流式响应转发
- 支持管理后台、上游 Key 存储和运行时配置更新
- 支持文件或 PostgreSQL 存储

## 目录结构

```text
cmd/jc_proxy/           程序入口
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

- 配置 `vendors.<name>.upstream.base_url`
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

- 默认会开启管理后台：`JC_PROXY_ADMIN_ENABLED=true`
- 若未显式提供管理员密码，首次启动会自动生成随机密码并打印到 Render 日志
- 此时即使还没有任何 vendor 配置，服务也能先启动，你可以登录 `/console/` 后再补充供应商和上游 Key

常用的 Render 环境变量：

```text
DATABASE_URL
JC_PROXY_STORAGE_MODE=pgsql
JC_PROXY_ADMIN_ENABLED=true
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

## 配置说明

主要配置可参考 [config.example.yaml](/home/xjc/jc_proxy/config.example.yaml)：

- `server`: HTTP 服务监听与超时配置
- `admin`: 管理后台账号、会话和访问控制
- `storage`: 配置与上游 Key 的存储方式
- `vendors`: 每个供应商的上游地址、鉴权、路径重写和错误策略

对流式接口，建议保持：

```yaml
server:
  write_timeout: 0s
```

这样可以避免长连接在固定时长后被服务端主动切断。

## 测试

```bash
go test ./...
```

## 备注

- 示例配置中的上游 Key 建议改为通过外部存储维护，不要直接提交到仓库
- 默认数据文件会写入 `./data/`
- 前端源码在 `web/`，运行时使用内嵌静态资源
