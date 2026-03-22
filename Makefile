.PHONY: dev build clean install

# 开发模式（前后端热重载）
dev:
	wails dev

# 构建生产版本
build:
	wails build

# 安装前端依赖
install:
	cd frontend && pnpm install

# 清理构建产物
clean:
	rm -rf build/bin frontend/dist
