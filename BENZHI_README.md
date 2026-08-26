# BENZHI_README

这是一个 Go 后端服务，用于编排城市无人驾驶车辆的任务调度、行程执行、安全处置、充电维护和持久化事件交付。

## 环境要求

- Go 1.25 或兼容的更新版本
- Docker 24 或更新版本（使用容器时）
- 无需外部数据库，服务使用内嵌 SQLite 文件并自动执行版本化迁移

## 标准构建、运行和测试命令

在仓库根目录执行：

```bash
# 编译
GOTOOLCHAIN=local go build ./...

# 启动
GOTOOLCHAIN=local go run ./cmd/server

# 测试
GOTOOLCHAIN=local go test ./... -count=1
GOTOOLCHAIN=local go test -race ./... -count=1
```

服务默认监听 `:8080`，数据库写入 `./data/autodrive.db`。可通过 `.env.example` 中列出的环境变量调整监听地址、数据库路径、会话有效期和 worker 租约。

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-autodrive-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-autodrive-arm64 linux/arm64
docker run -it benzhi-autodrive-amd64:latest
docker run -it --platform linux/arm64 benzhi-autodrive-arm64:latest
```

`benzhi.Dockerfile` 保留完整 Go 工具链并预下载依赖，进入容器后可以离线编译和运行测试。
