.PHONY: dev dev-sandbox dev-sandbox-down dev-sandbox-status run build build-embed install-app clean install build-cli install-cli build-ext lint test test-cover test-e2e test-e2e-scratch install-skill

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    BIN_PATH := ./build/bin/opskat.app/Contents/MacOS/opskat
else ifeq ($(UNAME_S),Linux)
    BIN_PATH := ./build/bin/opskat
else
    BIN_PATH := ./build/bin/opskat.exe
endif

VERSION ?= 1.0.0
APP_INSTALL_DIR ?= $(HOME)/Applications
COMMIT_ID := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION_PKG := github.com/cago-frame/cago/configs
BUILDINFO_PKG := github.com/opskat/opskat/internal/buildinfo
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(VERSION) -X $(BUILDINFO_PKG).CommitID=$(COMMIT_ID)

# 开发模式（前后端热重载）。用你自己的真实数据目录，供人手动开发/试用。
# 要做功能验证（尤其是会写数据的）请用 dev-sandbox，别在真实库上验。
dev:
	@mkdir -p frontend/dist && [ -e frontend/dist/.keep ] || touch frontend/dist/.keep
	wails dev

# 验证沙箱：在隔离数据目录上把真实应用跑起来并保持后台运行，附带一个无头 Chromium。
# 启动后用 e2e/drive.mjs 操作、e2e/oracle.mjs 读取副作用——一次性验证不再需要写 spec。
# 端口/数据目录按 checkout 分配，多个 worktree 可同时验证。流程见 docs/VERIFICATION.md。
# ARGS=--reset 清空沙箱数据，ARGS=--mocks 顺带起协议 mock，ARGS=--headed 显示浏览器。
dev-sandbox:
	node e2e/sandbox.mjs up $(ARGS)

dev-sandbox-down:
	node e2e/sandbox.mjs down $(ARGS)

dev-sandbox-status:
	node e2e/sandbox.mjs status

# 直接运行（不热重载）
run: build-embed
	$(BIN_PATH)

# 构建生产版本
build:
	wails build -ldflags="$(LDFLAGS)"

# 构建生产版本（内嵌 opsctl CLI）
build-embed: build-cli-embed
	wails build -ldflags="$(LDFLAGS)" -tags embed_opsctl

# 构建并安装 macOS 桌面应用（默认安装到当前用户的 ~/Applications）
ifeq ($(UNAME_S),Darwin)
install-app: build-embed
	@mkdir -p "$(APP_INSTALL_DIR)"
	@rm -rf "$(APP_INSTALL_DIR)/opskat.app"
	@ditto "./build/bin/opskat.app" "$(APP_INSTALL_DIR)/opskat.app"
	@echo "OpsKat installed to $(APP_INSTALL_DIR)/opskat.app"
else
install-app:
	@echo "install-app currently supports macOS only" >&2
	@exit 1
endif

# 构建 opsctl 用于嵌入桌面端
build-cli-embed:
	go build -ldflags="$(LDFLAGS)" -o ./internal/embedded/opsctl_bin ./cmd/opsctl/

# 安装前端依赖
install:
	cd frontend && pnpm install

# 构建 opsctl CLI
build-cli:
	go build -ldflags="$(LDFLAGS)" -o ./build/bin/opsctl ./cmd/opsctl/

# 安装 opsctl 到 GOPATH/bin
install-cli:
	go install -ldflags="$(LDFLAGS)" ./cmd/opsctl/

# 构建仓内示例扩展：WASI reactor（go build -buildmode=c-shared），产物连同 manifest /
# SKILL.md / locales 一起落到 extensions/$(EXT)/dist，那正是应用要装的目录形状。
# 装进正在运行的应用（沙箱见 docs/VERIFICATION.md）：
#   make build-ext && opsctl ext dev $(CURDIR)/extensions/$(EXT)/dist
# 重跑这两条就是热重载——Install 会先卸载旧模块。
EXT ?= notebook
build-ext:
	@rm -rf extensions/$(EXT)/dist
	@mkdir -p extensions/$(EXT)/dist
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o extensions/$(EXT)/dist/main.wasm ./extensions/$(EXT)
	@cp extensions/$(EXT)/manifest.json extensions/$(EXT)/SKILL.md extensions/$(EXT)/dist/
	@cp -R extensions/$(EXT)/locales extensions/$(EXT)/dist/
	@[ -d extensions/$(EXT)/frontend ] && cp -R extensions/$(EXT)/frontend/. extensions/$(EXT)/dist/ || true
	@echo "Built extensions/$(EXT)/dist — install it with: opsctl ext dev $(CURDIR)/extensions/$(EXT)/dist"

# 代码检查
lint:
	golangci-lint run --timeout 10m

# 代码检查并自动修复
lint-fix:
	golangci-lint run --timeout 10m --fix

# 运行测试
test:
	go test ./internal/... ./cmd/opsctl/... ./pkg/...

# E2E：Playwright 驱动真实 wails dev 跑 GUI 端到端。详见 docs/references/e2e-harness-guide.md。
# 一次性装依赖 + 浏览器：cd e2e && pnpm run setup（CI 在独立步骤里装，故这里不重复）。
# 配方只做 shell 无关的 cd && pnpm，跨平台(cmd/sh 皆可)；编排与收尾清理(回收残留
# vite、删临时目录)都在 e2e/run-e2e.mjs 里用 Node 跨平台完成。
test-e2e:
	cd e2e && pnpm test

# 临时功能验证：跑 e2e/scratch/ 里的一次性 spec（不提交）。约定/用法见 docs/references/e2e-harness-guide.md。
test-e2e-scratch:
	cd e2e && pnpm run test:scratch

# 测试覆盖率（生成 HTML 报告并在浏览器打开）
test-cover:
	go test -coverprofile=coverage.out ./internal/... ./cmd/opsctl/... ./pkg/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告已生成: coverage.html"
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || echo "请手动打开 coverage.html"

# 安装 Claude Code plugin（创建 symlink，注册 marketplace + plugin）
install-skill:
	@# 清理旧路径
	@rm -rf ~/.claude/skills/opsctl ~/.claude/plugins/cache/opskat
	@# Marketplace symlink → 市场根目录（含 .claude-plugin/marketplace.json + opsctl/ 插件目录）
	@rm -rf ~/.claude/plugins/marketplaces/opskat
	@mkdir -p ~/.claude/plugins/marketplaces
	@ln -s $(CURDIR)/plugin ~/.claude/plugins/marketplaces/opskat
	@# 注册到 installed_plugins.json + known_marketplaces.json + settings.json
	@python3 -c "\
	import json, os, datetime; \
	home = os.path.expanduser('~'); \
	now = datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.000Z'); \
	plugin_path = os.path.join(home, '.claude/plugins/marketplaces/opskat/opsctl'); \
	mkt_path = os.path.join(home, '.claude/plugins/marketplaces/opskat'); \
	key = 'opsctl@opskat'; \
	pf = os.path.join(home, '.claude/plugins/installed_plugins.json'); \
	cfg = json.load(open(pf)) if os.path.exists(pf) else {'version': 2, 'plugins': {}}; \
	entries = cfg['plugins'].get(key, []); \
	ue = [e for e in entries if e.get('scope') == 'user']; \
	(ue[0].update({'installPath': plugin_path, 'version': 'dev', 'lastUpdated': now}) if ue else \
	 entries.append({'scope': 'user', 'installPath': plugin_path, 'version': 'dev', 'installedAt': now, 'lastUpdated': now})); \
	cfg['plugins'].pop('opsctl@local', None); \
	cfg['plugins'][key] = entries; \
	json.dump(cfg, open(pf, 'w'), indent=2, ensure_ascii=False); \
	kf = os.path.join(home, '.claude/plugins/known_marketplaces.json'); \
	km = json.load(open(kf)) if os.path.exists(kf) else {}; \
	km['opskat'] = {'source': {'source': 'directory', 'path': mkt_path}, 'installLocation': mkt_path, 'lastUpdated': now}; \
	json.dump(km, open(kf, 'w'), indent=2, ensure_ascii=False); \
	sf = os.path.join(home, '.claude/settings.json'); \
	sc = json.load(open(sf)) if os.path.exists(sf) else {}; \
	sc.setdefault('enabledPlugins', {})[key] = True; \
	sc.setdefault('extraKnownMarketplaces', {})['opskat'] = {'source': {'source': 'directory', 'path': mkt_path}}; \
	json.dump(sc, open(sf, 'w'), indent=2, ensure_ascii=False); \
	print(f'Registered plugin: {key}')"
	@echo "Plugin installed: marketplace -> $(CURDIR)/plugin"

# 清理构建产物
clean:
	rm -rf build/bin frontend/dist internal/embedded/opsctl_bin \
		extensions/*/dist \
		coverage.out coverage.html coverage_new.out \
		opskat opsctl \
		frontend/package.json.md5
