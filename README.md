# 无人驾驶车队运营编排服务

这是一个面向城市无人驾驶运营团队的生产级 Go 后端。系统把运营区域、车辆、运输任务、调度行程、遥测告警、安全事件、充电资源、维护工单和事件投递组织为一致的持久化业务流程，而不是把它们拆成互不关联的 CRUD 接口。

核心流程包括：

- dispatcher 创建幂等任务，系统按区域、能力、安全有效期和电量筛选车辆，并在一个事务内占用车辆、更新任务、创建行程和审计记录。
- 行程开始与完成同步推进任务和车辆状态；完成时创建持久化 outbox，进程崩溃后仍可重新领取交付。
- 遥测按事件 ID 幂等写入；严重故障原子创建安全事件、审计与 outbox，safety_operator 通过带租约的认领和处置状态流完成闭环。
- 充电预约检查时间窗口冲突，开始和完成时协调车辆、连接器租约、充电会话及事件投递。
- 维护工单在打开时原子隔离车辆，只有全部检查项完成后才恢复先前可用状态。

## 环境与配置

需要 Go 1.25+。复制 `.env.example` 中的变量到运行环境即可；程序不会自动读取 dotenv 文件。默认配置适合本地启动，生产环境必须替换 bootstrap 密码并把数据库目录挂载到持久卷。

## 本地运行

```bash
go mod download
go run ./cmd/server
```

健康检查为 `GET /healthz`，依赖就绪检查为 `GET /readyz`。首次启动自动创建管理员，默认用户名 `admin`、密码 `change-me-now`。

## 测试和质量检查

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

测试使用临时真实 SQLite 数据库，覆盖 migration、事务回滚、并发所有权、重启恢复、会话撤销与过期、worker 重试取消、HTTP 错误映射和核心状态机。

## Docker

```bash
docker build --platform linux/amd64 -t autodrive-fleet-orchestrator:amd64 .
docker run --rm -p 8080:8080 -e AUTODRIVE_BOOTSTRAP_PASSWORD=strong-production-password autodrive-fleet-orchestrator:amd64
```

根 `Dockerfile` 的默认入口直接启动 `./cmd/server` 构建出的服务。`benzhi.Dockerfile` 与 `build_benzhi_docker.sh` 用于保留完整 Go 工具链的交互式评测环境。
