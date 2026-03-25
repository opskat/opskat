.PHONY: dev run build build-embed clean install build-cli install-cli lint test test-cover install-skill

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    BIN_PATH := ./build/bin/opskat.app/Contents/MacOS/opskat
else ifeq ($(UNAME_S),Linux)
    BIN_PATH := ./build/bin/opskat
else
    BIN_PATH := ./build/bin/opskat.exe
endif

VERSION ?= 1.0.0
VERSION_PKG := github.com/cago-frame/cago/configs
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(VERSION)

# 开发模式（前后端热重载）
dev:
	wails dev

# 直接运行（不热重载）
run: build-embed
	$(BIN_PATH)

# 构建生产版本
build:
	wails build -ldflags="$(LDFLAGS)"

# 构建生产版本（内嵌 opsctl CLI）
build-embed: build-cli-embed
	wails build -ldflags="$(LDFLAGS)" -tags embed_opsctl

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

# 代码检查
lint:
	golangci-lint run --timeout 10m

# 代码检查并自动修复
lint-fix:
	golangci-lint run --timeout 10m --fix

# 运行测试
test:
	go test ./internal/... ./cmd/opsctl/...

# 测试覆盖率（生成 HTML 报告并在浏览器打开）
test-cover:
	go test -coverprofile=coverage.out ./internal/... ./cmd/opsctl/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告已生成: coverage.html"
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || echo "请手动打开 coverage.html"

# 安装 Claude Code skill（创建 symlink 到 ~/.claude/skills/opsctl）
install-skill:
	@mkdir -p ~/.claude/skills
	@rm -f ~/.claude/skills/opsctl
	@ln -s $(CURDIR)/skill ~/.claude/skills/opsctl
	@echo "Skill installed: ~/.claude/skills/opsctl -> $(CURDIR)/skill"

# 清理构建产物
clean:
	rm -rf build/bin frontend/dist internal/embedded/opsctl_bin coverage.out coverage.html
