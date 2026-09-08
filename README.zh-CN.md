# Realy

[English](README.md) | 简体中文

Realy 是一个跨机器管理 AI Agent Runtime 的分布式控制平面。业务系统负责提交任务并提供业务能力；Realy 负责 Runtime 发现、调度、执行、隔离、持久化事件和结果。

```text
业务系统 / Host
        │ Go SDK 或 HTTP API
        ▼
Realy Server ── PostgreSQL / Artifact Store
        │ Node Protocol
        ▼
Realy Node ── Codex / Trae / 自定义 Runtime
        │
        └── Capability Binding: CLI / HTTP / RPC / Go
```

## 核心能力

- 多机器 Node 注册、心跳、容量和 Runtime Inventory
- 固定到指定 Runtime 实例，或根据 Provider 和 Capability 自动调度
- 原生 Codex、Trae Runtime 适配，支持流式输出和模型发现
- 持久化 Run、Attempt、Lease、重试、取消、超时和有序 SSE 事件
- 本地目录、临时目录、Git mirror 和 Git worktree Workspace Provider
- 本地卷或 S3 兼容对象存储 Artifact
- 通过进程、CLI、HTTP、RPC 或进程内方式接入业务 Capability
- Host/Node Token 分离，以及 Tenant/Project 隔离
- 内嵌 Web Agent Playground 和 `realyctl` 终端客户端
- 基于 PostgreSQL 的多 Node 协调和 Server 重启恢复

## 快速开始

### 1. 启动 Realy Server

Docker Compose 会启动 Realy Server 和 PostgreSQL。Server 启动时会自动执行数据库迁移。

```bash
git clone https://github.com/KDF5000/realy.git
cd realy
cp .env.example .env
```

在 `.env` 中设置数据库密码和两个相互独立的 Token：

```dotenv
POSTGRES_PASSWORD=replace-with-a-long-random-password
REALY_HOST_TOKEN=replace-with-a-long-random-host-token
REALY_NODE_TOKEN=replace-with-a-long-random-node-token
```

启动并验证：

```bash
docker compose up -d --build --wait
curl http://127.0.0.1:8787/health
```

预期响应：

```json
{"status":"ok"}
```

打开 <http://127.0.0.1:8787/console/>，首次访问时输入 `REALY_HOST_TOKEN`。

### 2. 接入 Node

在已经安装 Codex、`traex` 或 `trae-cli` 的机器上执行。Server 地址必须能从这台机器访问；远程 Server 不能使用 `127.0.0.1`。

```bash
curl -fsSL https://raw.githubusercontent.com/KDF5000/realy/main/install.sh \
  | REALY_NODE_TOKEN='与Server相同的Node Token' \
    sh -s -- --server https://realy.example.com --install-service
```

安装脚本支持 macOS/Linux 的 AMD64 和 ARM64。它会校验 Release 文件、将 `realy-node`、`realy-tool` 和 `realyctl` 安装到 `~/.local/bin`，从 `PATH` 自动发现 Runtime CLI，并把 Node 配置写入 `~/.config/realy/node.json`。

`--install-service` 会安装并启动用户级系统服务：

- Linux：systemd user service
- macOS：LaunchAgent

接入成功后，Node 及其 Runtime 会显示在控制台的 **Runtimes** 页面。

### 3. 创建 Agent

打开控制台的 **Agents** 页面，选择 Runtime 和模型，按需设置工作区，然后开始对话。Agent 可以：

- 固定到某个 Node 上的一个具体 Runtime 实例；或
- 在所有兼容 Runtime 实例之间自动调度。

## 部署与运维

### Railway

Railway 是临时将 Realy Control Plane 暴露到公网最简单的方式。新账号可以使用 Railway 的试用额度；由于 Realy Node 会持续发送心跳，Server 和 PostgreSQL 会保持活跃，请留意额度消耗。

1. 创建一个空的 Railway Project。
2. 通过 **New → Database → PostgreSQL** 添加 PostgreSQL。
3. 通过 **New → GitHub Repo** 添加另一个 Service，选择 `KDF5000/realy`。Railway 会自动识别仓库根目录的 `Dockerfile`。
4. 在 Realy Service 中设置以下变量：

   ```dotenv
   REALY_DATABASE_URL=${{Postgres.DATABASE_URL}}
   REALY_HOST_TOKEN=replace-with-a-long-random-host-token
   REALY_NODE_TOKEN=replace-with-a-different-long-random-node-token
   REALY_TENANT_ID=default
   REALY_PROJECT_ID=default
   REALY_ARTIFACT_BACKEND=file
   REALY_ARTIFACT_ROOT=/tmp/realy-artifacts
   ```

   Railway 会注入 `PORT`，Realy 将自动监听该端口。如果数据库 Service 不是 `Postgres`，需要相应修改引用变量中的名称。

5. 将 Health Check Path 设置为 `/health`，不要启用 Serverless/App Sleeping，然后在 **Settings → Networking** 中生成公网域名。
6. 验证服务并打开控制台：

   ```bash
   curl https://<service>.up.railway.app/health
   ```

   ```text
   https://<service>.up.railway.app/console/
   ```

临时验证时，文件 Artifact 会写入临时存储，在重新部署或重启后丢失。需要持久化 Artifact 时，请改用 `REALY_ARTIFACT_BACKEND=s3`；PostgreSQL 数据会独立持久化。

使用公网地址接入远程 Node：

```bash
curl -fsSL https://raw.githubusercontent.com/KDF5000/realy/main/install.sh \
  | REALY_NODE_TOKEN='与Server相同的Node Token' \
    sh -s -- --server https://<service>.up.railway.app --install-service
```

### Server 管理

```bash
docker compose ps
docker compose logs -f realy-server
docker compose down
```

`docker compose down` 不会删除 PostgreSQL 和 Artifact 数据卷。更新源码构建的部署：

```bash
git pull
docker compose up -d --build --wait realy-server
```

默认情况下，Realy Server 对外映射 `8787`，PostgreSQL 只绑定到 `127.0.0.1:55432`。生产环境建议在 `8787` 前配置 TLS 反向代理，不要把 PostgreSQL 暴露到公网。

Artifact 默认使用持久化 Docker 卷。也可以设置 `REALY_ARTIFACT_BACKEND=s3` 和 `REALY_S3_*` 环境变量，切换到 S3 兼容对象存储。

### Node 服务管理

Linux：

```bash
systemctl --user status realy-node
systemctl --user restart realy-node
journalctl --user -u realy-node -f
sudo loginctl enable-linger "$USER"
```

启用 linger 后，退出登录不会停止用户服务，并且无需交互登录也能随系统启动。

macOS：

```bash
launchctl print gui/$(id -u)/dev.realy.node
tail -f ~/.cache/realy/logs/node.log
```

不传 `--install-service` 时，可以让 Node 在前台运行：

```bash
~/.local/bin/realy-node -config ~/.config/realy/node.json
```

常用安装参数包括 `--node-id`、`--capacity`、`--runtime auto|codex|trae|both`、`--version`、`--install-dir`、`--config` 和 `--force`。默认保留已有配置；传入 `--force` 时会先创建带时间戳的备份。

## 终端客户端

可以从源码构建 `realyctl`，也可以直接使用 `install.sh` 安装的二进制：

```bash
make build-realyctl
./bin/realyctl runtime list
./bin/realyctl run submit --provider codex --prompt "检查当前代码仓库"
./bin/realyctl run submit --provider codex --runtime-id developer-node/codex --prompt "固定到指定 Runtime"
./bin/realyctl run submit --provider codex --model model-a --prompt "使用指定模型"
./bin/realyctl run submit --provider codex --max-attempts 2 --retry-backoff 2s --prompt "执行可恢复任务"
./bin/realyctl run watch <run-id>
./bin/realyctl run cancel <run-id> --reason "不再需要"
./bin/realyctl run list
./bin/realyctl run attempts <run-id>
./bin/realyctl run artifacts <run-id>
./bin/realyctl artifact download <artifact-id> ./result.bin
./bin/realyctl run interactions <run-id>
./bin/realyctl interaction resolve <interaction-id> '{"approved":true}'
```

通过 `REALY_SERVER_URL` 或全局 `--server` 参数连接其他 Control Plane。`run submit` 默认持续接收有序 SSE 事件；使用 `--watch=false` 可以在提交后立即返回。

## 核心概念

### Run 与 Lease

任务提交后成为 Run。兼容的 Node 会原子领取 Attempt 并获得可续期 Lease。Lease fencing 会阻止旧 Worker 完成已经重新分配的任务。Node 失联后，Reconciler 会把 Attempt 标记为 `lost`，并按照 Run 的重试策略处理。

取消和超时使用同一条持久化路径：Server 记录请求，Node 在续租时收到请求，终止 Runtime 进程并确认最终状态。

### Workspace

Agent 可以使用临时目录、已有本地目录、Git mirror 或隔离 worktree。固定到远程 Node 的 Runtime 会在该 Node 上解析本地工作区路径，而不是在 Server 上解析。

### Capability

业务系统可以暴露领域操作，而不需要把领域语义加入 Realy Core。Runtime 调用 `realy-tool`，Realy 校验 Run Grant 和资源范围，再调用配置的 Binding。

支持的 Binding 方式包括：

- 业务方提供的 CLI 进程；
- HTTP 接口；
- RPC 适配器；
- 进程内 Go Provider。

`realy-tool` 依赖 Node 注入的短生命周期 Run 级环境变量。业务凭据只保留在对应 Binding 内，不直接暴露给 Agent Runtime。

### Event 与 Interaction

事件会先持久化并分配有序 Sequence，再发送给客户端。SSE 接口同时支持 `after={sequence}` 和标准 `Last-Event-ID` 请求头，因此断线重连不会丢失或重复事件。

长时间运行的 Runtime 可以创建审批或输入 Interaction，暂停执行，并在 Host 处理后恢复。

## Runtime 说明

- Codex 和 Trae 使用 `"protocol": "app-server"` 产生增量 `assistant.message.delta` 事件并自动发现模型。
- 旧的 `exec` 协议仍可兼容非交互式 CLI，但只能返回完整消息。
- Trae exec 模式不能使用 `permission_mode=default`，因为无头进程无法请求审批。可以省略该配置使用 headless 默认值，或使用 `bypass_permissions`/兼容无头执行的 `custom` 策略。
- Runtime 子进程默认只继承受限环境。额外变量需要在 Node 配置中通过 `pass_env` 或 `env` 显式声明。

## 开发与验证

开发环境需要 Go 1.26，以及支持 Compose 的 Docker。

```bash
make verify
make demo
make codex-smoke
make trae-smoke
make codex-capability-smoke
```

`codex-smoke`、`trae-smoke` 和 `codex-capability-smoke` 会调用本机已经登录的 AI Runtime，可能产生 Provider 用量。

使用 PostgreSQL 从源码启动 Server：

```bash
make db-up
export REALY_DATABASE_URL='postgres://realy:realy@127.0.0.1:55432/realy?sslmode=disable'
go run ./cmd/realy-server -listen 127.0.0.1:8787
make postgres-test
```

`-memory` 仅用于测试和临时 Demo；生产部署需要 PostgreSQL。

启用鉴权时必须同时配置 `REALY_HOST_TOKEN` 和 `REALY_NODE_TOKEN`。`realyctl` 读取 `REALY_HOST_TOKEN`；Node 从配置文件或 `REALY_NODE_TOKEN` 读取 Token。

Web Agent Playground 内嵌在 Server 二进制中。修改 `transport/httpapi/console/` 后需要重新构建或重启 `realy-server`，让 Go 重新嵌入静态资源。Agent Profile 和对话索引目前保存在浏览器 localStorage；Run、Event、Result 和 Artifact 由 Realy API 持久化。

推送 `v*` Tag 会运行 [Release workflow](.github/workflows/release.yml)，验证项目并发布 Linux/macOS、AMD64/ARM64 的带校验和压缩包。

## 文档

- [架构与设计](docs/design.md)
- [Node 配置示例](examples/realy-node.example.json)
- [Releases](https://github.com/KDF5000/realy/releases)

首个 Host 集成是 Multica Adapter。它通过 `multica capability invoke --protocol realy-v1` 暴露 `issue.read@1`，而 Realy Core 不需要理解 Issue 或项目管理语义。
