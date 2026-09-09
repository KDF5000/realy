# Realy

English | [简体中文](README.zh-CN.md)

Realy is an SDK and distributed execution component for AI agent runtimes across machines. Applications own their agents, workflows, and business logic; Realy provides runtime discovery, scheduling, execution, workspace preparation, persisted events, and results. The included Web Playground is a reference application for validating integrations.

```text
Application / Host
        │ Go SDK or HTTP API
        ▼
Realy Server ── PostgreSQL / Artifact Store
        │ Node Protocol
        ▼
Realy Node ── Codex / Trae / Custom Runtime
        │
        └── Capability Binding: CLI / HTTP / RPC / Go
```

## Highlights

- Multi-machine Node registration, heartbeats, capacity, and runtime inventory
- Fixed runtime-instance assignment or automatic scheduling by provider and capability
- Native Codex and Trae runtime adapters with streaming output and model discovery
- Durable runs, attempts, leases, retries, cancellation, timeouts, and ordered SSE events
- Local, temporary, Git mirror, and Git worktree workspace providers
- Artifact storage on local volumes or S3-compatible object storage
- Application-defined capabilities through process, CLI, HTTP, RPC, or in-process bindings
- Host/Node token separation plus tenant and project isolation
- Embedded Web Agent Playground reference client and the `realyctl` terminal client
- PostgreSQL-backed coordination for multiple Nodes and Server restarts

## Quick start

### 1. Start Realy Server

Docker Compose starts Realy Server and PostgreSQL. Schema migrations run automatically when the Server starts.

```bash
git clone https://github.com/KDF5000/realy.git
cd realy
cp .env.example .env
```

Set a database password and two independent tokens in `.env`:

```dotenv
POSTGRES_PASSWORD=replace-with-a-long-random-password
REALY_HOST_TOKEN=replace-with-a-long-random-host-token
REALY_NODE_TOKEN=replace-with-a-long-random-node-token
```

Start the stack and verify it:

```bash
docker compose up -d --build --wait
curl http://127.0.0.1:8787/health
```

The expected response is:

```json
{"status":"ok"}
```

Open the Web Playground at <http://127.0.0.1:8787/console/> and enter `REALY_HOST_TOKEN` when prompted.

### 2. Connect a Node

Run this on a machine that already has Codex, `traex`, or `trae-cli` installed. Replace the Server URL with an address reachable from that machine; do not use `127.0.0.1` for a remote Server.

```bash
curl -fsSL https://raw.githubusercontent.com/KDF5000/realy/main/install.sh \
  | REALY_NODE_TOKEN='the-same-node-token-as-the-server' \
    sh -s -- --server https://realy.example.com --install-service
```

The installer supports macOS and Linux on AMD64 and ARM64. It verifies the release checksum, installs `realy-node`, `realy-tool`, and `realyctl` under `~/.local/bin`, discovers supported runtime CLIs from `PATH`, and writes the Node configuration to `~/.config/realy/node.json`.

`--install-service` installs and starts a user-level system service:

- Linux: systemd user service
- macOS: LaunchAgent

Once connected, the Node and its runtimes appear in the Playground's **Runtimes** view.

### 3. Create an Agent

Open **Agents** in the Web Playground, select a runtime and model, configure a workspace if needed, and start a conversation. An Agent can either:

- bind to one exact runtime instance on one Node; or
- use automatic scheduling across compatible runtime instances.

The Playground's Agent profiles, conversation index, and chat UX are demonstration-level application state stored in the browser. They are not Realy Core entities or a persistence contract. A production Host should own its Agent definitions, conversations, permissions, and workflow state, and submit execution Requests to Realy.

## Embed with the Go SDK

Applications can use the HTTP transport directly or wrap it with the convenience SDK client:

```go
package main

import (
    "context"
    "log"

    "github.com/KDF5000/realy"
    "github.com/KDF5000/realy/sdk"
    "github.com/KDF5000/realy/transport/httpapi"
)

func main() {
    ctx := context.Background()
    client := sdk.New(httpapi.NewAuthenticatedClient(
        "https://realy.example.com",
        "host-token",
    ))

    run, err := client.Submit(ctx, realy.Request{
        AgentID:        "code-reviewer",
        IdempotencyKey: "review-42",
        Runtime:        realy.RuntimeRequirement{Provider: "codex"},
        Input:          realy.Input{Type: "task", Version: "1", Prompt: "Review change 42"},
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("queued run %s", run.ID)
}
```

Prefer the smallest interface needed by application code: `sdk.Submitter`, `sdk.Runs`, `sdk.Events`, `sdk.Artifacts`, or `sdk.Interactions`. `sdk.Backend` composes the complete surface for the convenience client. This keeps business adapters independent from unrelated Realy features.

## Deployment

### Railway

Railway is the simplest way to put a temporary Realy control plane on the public internet. New accounts can use Railway's trial credits; keep an eye on usage because Realy Node heartbeats keep the Server and PostgreSQL active.

1. Create an empty Railway project.
2. Add a PostgreSQL database with **New → Database → PostgreSQL**.
3. Add another service with **New → GitHub Repo** and select `KDF5000/realy`. Railway detects the root `Dockerfile` automatically.
4. Add these variables to the Realy service:

   ```dotenv
   REALY_DATABASE_URL=${{Postgres.DATABASE_URL}}
   REALY_HOST_TOKEN=replace-with-a-long-random-host-token
   REALY_NODE_TOKEN=replace-with-a-different-long-random-node-token
   REALY_TENANT_ID=default
   REALY_PROJECT_ID=default
   REALY_ARTIFACT_BACKEND=file
   REALY_ARTIFACT_ROOT=/tmp/realy-artifacts
   ```

   Railway injects `PORT`; Realy listens on it automatically. If the database service has a different name, replace `Postgres` in the reference variable.

5. Set the health check path to `/health`, leave Serverless/App Sleeping disabled, and generate a public domain under **Settings → Networking**.
6. Verify the deployment and open the Playground:

   ```bash
   curl https://<service>.up.railway.app/health
   ```

   ```text
   https://<service>.up.railway.app/console/
   ```

For a temporary validation deployment, file artifacts are written to ephemeral storage and disappear after a redeploy or restart. Use `REALY_ARTIFACT_BACKEND=s3` for durable artifacts. PostgreSQL remains persistent independently.

Connect remote Nodes with the public URL:

```bash
curl -fsSL https://raw.githubusercontent.com/KDF5000/realy/main/install.sh \
  | REALY_NODE_TOKEN='the-same-node-token-as-the-server' \
    sh -s -- --server https://<service>.up.railway.app --install-service
```

### Server operations

```bash
docker compose ps
docker compose logs -f realy-server
docker compose down
```

`docker compose down` preserves PostgreSQL and Artifact volumes. To update a source-built deployment:

```bash
git pull
docker compose up -d --build --wait realy-server
```

By default, Realy Server is published on port `8787`, while PostgreSQL is bound only to `127.0.0.1:55432`. In production, place a TLS reverse proxy in front of port `8787` and avoid exposing PostgreSQL publicly.

Artifacts use a persistent Docker volume by default. Realy Server can also use S3-compatible storage through `REALY_ARTIFACT_BACKEND=s3` and the `REALY_S3_*` environment variables.

### Node service operations

Nodes persist unacknowledged events before sending. On restart, they replay events
under valid leases and mark interrupted attempts as failed; this does not resume
the agent process. Stale events are discarded with a log entry. Unknown network
outcomes retain the files and prevent startup from claiming new work.

The default spool is under the OS user configuration directory, in
`realy/outbox/<server-node-hash>`. Optional Node JSON settings `outbox_root`
(base directory; the identity subdirectory is always added) and `outbox_max_bytes`
(default 67108864) control its location and capacity. Capacity exhaustion fails
the execution rather than growing without bound. Preserve this directory across
Node updates; it contains lease credentials and private event data.

Linux:

```bash
systemctl --user status realy-node
systemctl --user restart realy-node
journalctl --user -u realy-node -f
sudo loginctl enable-linger "$USER"
```

Enabling linger keeps the user service running after logout and starts it without an interactive login.

macOS:

```bash
launchctl print gui/$(id -u)/dev.realy.node
tail -f ~/.cache/realy/logs/node.log
```

Run the installer without `--install-service` to keep the Node in the foreground:

```bash
~/.local/bin/realy-node -config ~/.config/realy/node.json
```

Useful installer options include `--node-id`, `--capacity`, `--runtime auto|codex|trae|both`, `--version`, `--install-dir`, `--config`, and `--force`. Existing configuration is preserved unless `--force` is set, in which case the installer creates a timestamped backup.

## Terminal client

Build `realyctl` from source, or use the binary installed by `install.sh`:

```bash
make build-realyctl
./bin/realyctl runtime list
./bin/realyctl run submit --provider codex --prompt "Inspect this repository"
./bin/realyctl run submit --provider codex --runtime-id developer-node/codex --prompt "Run on this exact runtime"
./bin/realyctl run submit --provider codex --model model-a --prompt "Use a specific model"
./bin/realyctl run submit --provider codex --max-attempts 2 --retry-backoff 2s --prompt "Retry recoverable work"
./bin/realyctl run watch <run-id>
./bin/realyctl run cancel <run-id> --reason "No longer needed"
./bin/realyctl run list
./bin/realyctl run attempts <run-id>
./bin/realyctl run artifacts <run-id>
./bin/realyctl artifact download <artifact-id> ./result.bin
./bin/realyctl run interactions <run-id>
./bin/realyctl interaction resolve <interaction-id> '{"approved":true}'
```

Set `REALY_SERVER_URL` or pass the global `--server` option to connect to another control plane. `run submit` watches ordered SSE events by default; use `--watch=false` to return after submission.

## Core concepts

### Runs and leases

A submitted task becomes a Run. A compatible Node atomically claims an Attempt and receives a renewable lease. Lease fencing prevents stale workers from completing reassigned work. If a Node disappears, the reconciler marks the Attempt as lost and applies the Run's retry policy.

Cancellation and timeouts use the same durable path: the Server records the request, the Node receives it during lease renewal, terminates the runtime process, and acknowledges the final state.

### Workspaces

Agents may use temporary directories, existing local directories, Git mirrors, or isolated worktrees. A runtime fixed to a remote Node resolves local workspace paths on that Node, not on the Server.

### Capabilities

Applications expose domain operations without adding domain semantics to Realy Core. A runtime calls `realy-tool`, Realy validates the Run grant and resource scope, and then invokes the configured binding.

Supported binding styles include:

- application-provided CLI processes;
- HTTP endpoints;
- RPC adapters; and
- in-process Go providers.

`realy-tool` relies on short-lived Run-scoped environment values injected by the Node. Business credentials remain inside the selected binding and are not exposed directly to the agent runtime.

### Events and interactions

Events are persisted before delivery and receive an ordered sequence number. The SSE endpoint supports both `after={sequence}` and the standard `Last-Event-ID` header, allowing clients to reconnect without losing or duplicating events.

A stream is complete only after a terminal `run.succeeded`, `run.failed`, or `run.cancelled` event. The Go HTTP client reports an interrupted stream when the connection ends earlier, so Hosts can reconnect from the last processed sequence. A complete assistant message alone is not proof that a Run completed.

Long-running runtimes can create approval or input interactions, pause, and resume after a Host resolves them.

## Runtime notes

- Codex and Trae use `"protocol": "app-server"` for incremental `assistant.message.delta` events and runtime model discovery.
- App-server execution succeeds only after the runtime reports `turn/completed`; partial output is retained as events when the protocol ends early, but the Run fails.
- The legacy `exec` protocol remains available for compatible non-interactive CLIs but only produces complete messages.
- Trae exec mode cannot use `permission_mode=default`, because a headless process cannot ask for approval. Omit it for the headless default, or use `bypass_permissions` or a headless-compatible `custom` policy.
- Runtime subprocesses receive a restricted environment by default. Add variables explicitly through `pass_env` or `env` in the Node configuration.

## Development

Requirements: Go 1.26 and Docker with Compose.

```bash
make verify
make demo
make codex-smoke
make trae-smoke
make codex-capability-smoke
```

`codex-smoke`, `trae-smoke`, and `codex-capability-smoke` invoke locally authenticated AI runtimes and may consume provider usage.

Run the Server from source with PostgreSQL:

```bash
make db-up
export REALY_DATABASE_URL='postgres://realy:realy@127.0.0.1:55432/realy?sslmode=disable'
go run ./cmd/realy-server -listen 127.0.0.1:8787
make postgres-test
```

Use `-memory` only for tests and temporary demos. Production deployments require PostgreSQL.

When authentication is enabled, both `REALY_HOST_TOKEN` and `REALY_NODE_TOKEN` must be configured. `realyctl` reads `REALY_HOST_TOKEN`; a Node reads the token from its configuration or `REALY_NODE_TOKEN`.

The Web Agent Playground is embedded into the Server binary. After changing files under `transport/httpapi/console/`, rebuild or restart `realy-server` so Go can embed the updated assets. Runs, events, results, and artifacts are persisted by the Realy API.

Pushing a `v*` tag runs the [release workflow](.github/workflows/release.yml), verifies the project, and publishes checksum-protected archives for Linux and macOS on AMD64 and ARM64.

## Documentation

- [Component boundaries and execution guarantees (Chinese)](docs/component-contract.md)
- [Architecture and design](docs/design.md)
- [Example Node configuration](examples/realy-node.example.json)
- [Releases](https://github.com/KDF5000/realy/releases)

The first Host integration is the Multica adapter. It exposes `issue.read@1` through `multica capability invoke --protocol realy-v1`, while Realy Core remains unaware of issue or project-management semantics.
