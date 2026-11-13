.DEFAULT_GOAL := help

GO ?= go
GOFMT ?= gofmt
NPM ?= npm
CONFIG ?= ./config.yaml
BENCHTIME ?= 300ms
BENCH_COUNT ?= 3

.PHONY: help deps web-deps web build build-server build-upgrade run \
	test test-go test-web test-race vet check fmt bench-plugins clean

help:
	@printf '%s\n' \
		'make deps           下载 Go 和前端依赖' \
		'make build          构建前端、网关和升级工具，二进制输出到 bin/' \
		'make web            重新构建内嵌前端' \
		'make run            构建并启动网关，CONFIG=./config.yaml 可覆盖配置路径' \
		'make test           运行 Go 和前端测试' \
		'make test-go        运行 Go 全量测试' \
		'make test-web       运行前端测试' \
		'make test-race      运行 Go 全量 race 检查' \
		'make vet            运行 go vet' \
		'make check          运行测试和 go vet' \
		'make fmt            格式化 Go 源码' \
		'make bench-plugins  运行插件性能基准，可覆盖 BENCHTIME / BENCH_COUNT' \
		'make clean          清理构建产物，保留配置、数据和已安装依赖'

deps: web-deps
	$(GO) mod download

web-deps: web/node_modules/.make-deps

# npm ci only runs on first use or when the dependency manifests change.
web/node_modules/.make-deps: web/package.json web/package-lock.json
	$(NPM) --prefix web ci
	touch "$@"

# Always rebuild the UI before Go embeds it, even if file timestamps are unchanged.
web: web-deps
	$(NPM) --prefix web run build

build: build-server build-upgrade

build-server: web
	mkdir -p bin
	$(GO) build -o bin/jc_proxy ./cmd/jc_proxy

build-upgrade:
	mkdir -p bin
	$(GO) build -o bin/jc_proxy_upgrade ./cmd/jc_proxy_upgrade

run: build-server
	./bin/jc_proxy -config "$(CONFIG)"

test: test-go test-web

test-go: web
	$(GO) test ./...

test-web: web-deps
	$(NPM) --prefix web test

test-race: web
	$(GO) test -race ./...

vet: web
	$(GO) vet ./...

check: test vet

fmt:
	$(GOFMT) -w cmd internal

bench-plugins: web
	$(GO) test ./internal/gateway -run '^$$' -bench '^BenchmarkRequestPlugins$$' \
		-benchmem -cpu=1 -benchtime="$(BENCHTIME)" -count="$(BENCH_COUNT)"

clean:
	rm -rf -- bin internal/ui/dist web/dist web/.vite
