# Realy

Realy 是一个多机器 Agent Runtime 管理平台。业务系统只负责提交任务、声明业务能力并
消费结果；Realy 负责 Node、Runtime、调度、执行、权限边界和事件。

```text
Multica / 其他业务系统
        │ Host SDK
        ▼
Realy Control Plane
        │ Node Protocol
        ▼
Realy Node ──> Codex / Claude / 自定义 Runtime
        │
        └── Capability Binding: CLI / HTTP / RPC / Go
```

## 安装 Realy Node

macOS 或 Linux 可以从 GitHub Release 一条命令安装。通过 `--server` 指定 Node 要连接的
Realy Server：

```bash
curl -fsSL https://raw.githubusercontent.com/KDF5000/realy/main/install.sh \
  | sh -s -- --server https://realy.example.com
```

脚本支持 Intel/Apple Silicon 和 Linux amd64/arm64，下载后会验证 SHA-256，并安装
`realy-node`、`realy-tool` 和 `realyctl` 到 `~/.local/bin`。它会自动发现 PATH 中的 Codex、
`traex` 或 `trae-cli`，配置默认写入 `~/.config/realy/node.json`，运行数据放在
`~/.cache/realy`。随后启动：

```bash
~/.local/bin/realy-node -config ~/.config/realy/node.json
```

启用 Node Token 时建议通过环境变量传递，避免 Token 出现在 Shell 历史中：

```bash
curl -fsSL https://raw.githubusercontent.com/KDF5000/realy/main/install.sh \
  | REALY_NODE_TOKEN=node-secret sh -s -- --server https://realy.example.com
```

可以使用 `--node-id`、`--capacity`、`--runtime auto|codex|trae|both`、`--version`、
`--install-dir` 和 `--config` 自定义安装。重复安装默认保留现有配置；传入 `--force` 时会先
创建带时间戳的备份再生成新配置。

推送 `v*` Tag 后，[Release workflow](.github/workflows/release.yml) 会运行测试并发布四个平台
压缩包及 `checksums.txt`：

```bash
git tag v0.1.0
git push origin v0.1.0
```

## 当前可验证能力

- Node 注册、心跳、容量和 Runtime Inventory
- 支持固定 Runtime 实例，或根据 Provider、Node Label 和 Capability 自动调度任务
- Run、Attempt、Lease 和有序事件
- Host SDK 与 HTTP Transport
- 用户自定义 Exec、HTTP、RPC、进程内 Capability Binding
- Run 级 Grant、资源范围和并发幂等检查
- Runtime 本地 Tool Bridge，以及统一的 `realy-tool`
- 通用非交互式 Runtime Command Adapter
- 真实 Codex Runtime：`codex exec`、JSONL 事件、最终消息与 sandbox
- 真实 TraeCode Runtime：`traex`/`trae-cli exec`、JSONL、最终消息、权限模式与 sandbox
- `realyctl` 终端工作台：Runtime Inventory、Run 下发、状态与事件跟踪
- PostgreSQL Control Plane Store：持久化 Node、Run、Attempt、Event 和原子任务领取
- 运行中 Lease 自动续租、Node 离线状态和过期 Attempt 恢复
- 显式 `max_attempts`/`backoff` 重试策略，以及旧 Lease fencing
- 持久化任务取消、运行超时和 Node 终止确认
- 基于有序 Event Sequence 的 SSE 实时事件流和断线续传
- Capacity 并发执行、优雅 drain 和 Unix 进程组回收
- temp/local/Git mirror + worktree Workspace Provider
- Artifact 上传、下载、SHA-256、本地文件与 S3 兼容 Blob Store
- PostgreSQL Capability Call reservation、重放和审计
- Host/Node Token、Tenant/Project 隔离
- Run 列表、Attempt 历史、Artifact 与 Interaction API
- Runtime 版本约束与周期健康探测
- Session ID，以及通用 Input/Approval 阻塞恢复模型
- Multica 源码级 Host Adapter 和 `multica capability invoke` Binding
- PostgreSQL + HTTP 多 Node 失联恢复 E2E

## 验证

```bash
make verify
make demo
make codex-smoke
make trae-smoke
make codex-capability-smoke
```

生产 Control Plane 默认要求 PostgreSQL；`-memory` 只用于测试和临时 demo：

```bash
make db-up
export REALY_DATABASE_URL='postgres://realy:realy@127.0.0.1:55432/realy?sslmode=disable'
go run ./cmd/realy-server -listen 127.0.0.1:8787
make postgres-test
```

启用鉴权（两类 Token 必须同时配置）：

```bash
export REALY_HOST_TOKEN=host-secret
export REALY_NODE_TOKEN=node-secret
export REALY_TENANT_ID=local
export REALY_PROJECT_ID=multica
```

Node 配置 `token` 或读取 `REALY_NODE_TOKEN`；`realyctl` 读取 `REALY_HOST_TOKEN`。Artifact
默认写入 `./.realy/artifacts`，也可用 `-artifact-backend s3` 和 `REALY_S3_*` 切换到
S3/MinIO。

Demo 会启动临时 Control Plane 和 Multica Capability API，注册两个不同 Runtime 的
Node，验证调度器不会把任务发给不兼容的 Node，然后由匹配 Node 完成任务。

`make codex-smoke` 会使用本机已有的 Codex CLI 登录态执行一次真实的只读任务，因此会
产生一次实际 Codex 用量。成功时 Result Summary 为 `REALY_CODEX_OK`。

`make trae-smoke` 使用本机 TraeCode CLI 登录态执行同样的真实只读链路，会产生一次实际
Trae 用量。它优先使用 `traex`，Node 运行时在未安装该别名时会回退到 `trae-cli`；成功时
Result Summary 为 `REALY_TRAE_OK`。

Trae `exec` 不能使用会发起交互审批的 `permission_mode=default`。配置中应省略该字段以
采用 headless 默认值，或明确使用 `bypass_permissions`/`custom`；Realy 会自动把旧的
`default` 配置归一化为省略。

Codex/Trae 的逐 token 输出使用 Runtime 配置 `"protocol": "app-server"`。适配器会将
不同 Provider 的通知归一化为 `assistant.message.delta` 事件；`exec` 协议仍可用于兼容
只产生完整消息的旧 CLI，但无法提供真正的文本增量。

`make codex-capability-smoke` 会进一步验证完整调用链：真实 Codex 执行 `realy-tool`，
Realy 校验 Run Grant 和资源范围，再调用用户提供的 CLI Binding。它同样会产生实际
Codex 用量，成功时事件中必须出现 `capability.succeeded`。

## 可执行程序

```bash
go run ./cmd/realy-server -listen :8787 # 需要 REALY_DATABASE_URL
go run ./cmd/realy-node -config ./examples/realy-node.example.json
```

### Web Agent Playground

Playground 已嵌入 `realy-server`，不需要单独启动前端开发服务器。Control Plane 和
Node 启动后访问 <http://127.0.0.1:8787/console/>。启用鉴权时，页面首次打开会要求
输入 `REALY_HOST_TOKEN`，Token 会写入 HttpOnly Cookie，不保存在前端存储中。

Agent Profile 和对话索引目前保存在浏览器 localStorage；真实 Run、事件、结果和产物仍
由 Realy API 持久化到 PostgreSQL。修改 `transport/httpapi/console/` 下的前端文件后需
重启 `realy-server`，Go embed 才会包含最新资源。

Agent 创建时可以固定到某个 Runtime 实例，也可以选择按 Provider 自动调度。固定实例离线或
容量已满时，任务会继续等待该实例，不会自动切换到其他机器；自动调度则可由任意兼容且可用
的实例领取。Agent 还可覆盖 Runtime 的默认模型。Codex 和 Trae Node 启动时会通过 `app-server model/list`
自动发现模型并向控制台公开；配置中的 `model` 可覆盖默认值，`models` 可补充额外模型，
Agent Playground 只允许选择 Runtime 已公开的模型。

构建并使用终端工作台：

```bash
make build-realyctl
./bin/realyctl runtime list
./bin/realyctl run submit --provider codex --prompt "检查当前工作目录"
./bin/realyctl run submit --provider codex --runtime-id developer-macbook/codex --prompt "固定到指定 Runtime"
./bin/realyctl run submit --provider codex --model model-a --prompt "使用指定模型执行"
./bin/realyctl run submit --provider codex --max-attempts 2 --retry-backoff 2s --prompt "执行可恢复任务"
./bin/realyctl run submit --provider codex --timeout 30m --prompt "执行限时任务"
./bin/realyctl run submit --prompt "读取 MUL-42" --grant issue.read@1:read:MUL-42
./bin/realyctl run watch <run-id>
./bin/realyctl run cancel <run-id> --reason "不再需要"
./bin/realyctl run list
./bin/realyctl run attempts <run-id>
./bin/realyctl run artifacts <run-id>
./bin/realyctl artifact download <artifact-id> ./result.bin
./bin/realyctl run interactions <run-id>
./bin/realyctl interaction resolve <interaction-id> '{"approved":true}'
```

`run submit` 默认通过 SSE 持续输出有序事件并等待完成；使用 `--watch=false` 只下发任务。通过
`REALY_SERVER_URL` 或全局 `--server` 可以连接其他机器上的 Control Plane。
`run watch` 使用最后收到的 Sequence 自动续传，重连不会重复输出或遗漏已持久化事件。
Node 执行期间会独立发送心跳并自动续租 Assignment；`runtime list` 的 `STATE` 列根据
最后心跳显示 `online` 或 `offline`。运行中 Lease 过期时，Control Plane 会把旧 Attempt
标记为 `lost`，并按任务声明的重试策略创建新的 Attempt。

取消请求会先持久化为 `cancelling`，再通过下一次 Lease 续租下发给 Node。Node 停止
Runtime 后确认取消，最终状态变为 `cancelled`；Node 失联时由 Reconciler 在 Lease 过期后
完成回收。`--timeout` 使用同一条取消路径。

SSE 接口为 `GET /v1/runs/{run_id}/events/stream?after={sequence}`，同时支持标准
`Last-Event-ID` 请求头。事件先持久化到 PostgreSQL，再向客户端发送，因此 Server 重启或
连接中断不会丢失事件。

`realy-tool` 由 Runtime 子进程调用，例如
`realy-tool call --resource MUL-42 --idempotency read-1 issue.read`。它依赖 Node 为当前
Run 注入的短生命周期环境变量，不能作为普通终端命令脱离 Run 使用。业务 CLI、HTTP
Token 等不会直接暴露给 Agent。

Codex Runtime 使用工作目录内的私有文件邮箱进行桥接，以兼容禁止本机网络访问的
Codex sandbox；其他 Runtime 仍可使用 loopback HTTP Bridge。两者对 Agent 暴露相同的
`realy-tool` 命令和 Capability 协议。

Codex 子进程默认使用环境变量白名单，不继承 Node 的完整环境；Multica Token 等业务
凭据只传给对应 Binding。额外 Runtime 环境必须通过 Node 配置里的 `pass_env` 或 `env`
显式声明。

Multica 已提供首个宿主适配入口：

```bash
multica capability invoke --protocol realy-v1
```

该命令从 stdin 接收 Realy `CapabilityRequest`，向 stdout 返回 `{"output": ...}` 或
`{"error": ...}`。当前实现 `issue.read@1`，因此 Realy Core 不需要知道 issue、workspace
或 Multica API 的任何语义。

完整设计见 [docs/design.md](docs/design.md)。
