# Playground Deployment And Verification

This document is an obsolete external-environment snapshot. The repository
cannot verify the state or paths of the shared playground described here. Use
the repository Compose files and current v2 CLI/API documentation for supported
deployments; this is not a repository-local development environment.

Historical environment snapshot (not verified by this repository):

- Code directory: `/data/code`
- Deployment directory: `/data/playground`
- Compose file: `/data/playground/docker-compose.yml`
- Current shared compose deploys the `agent-compose` daemon and the independent
  `agent-compose-ui` frontend service

For local integration testing, use the repository-root `docker-compose.yml`.
Do not mix this shared playground document with the local compose setup inside
the repo.

## Prerequisites

- Docker and `docker compose` are available on the host.
- `/dev/kvm` exists and is usable only when this playground selects the BoxLite
  or Microsandbox runtime. The current `RUNTIME_DRIVER=docker` path does not
  require KVM.
- The host allows containers to mount `/var/run/docker.sock`.
- The `/data/code` repository exists locally.
- The build machine can access image and dependency sources needed by the build,
  or the corresponding layers already exist in the host cache.

## Current Daemon Compose Facts

Current key configuration for the shared playground `agent-compose` daemon
service:

- Listen port: `7410`
- `DATA_ROOT=/data`
- `SANDBOX_ROOT=/data/sandboxes`
- `DOCKER_HOST_SANDBOX_ROOT=/data/playground/data/agent-compose/sandboxes`
- `RUNTIME_DRIVER=docker`
- `DEFAULT_IMAGE=${DEFAULT_IMAGE:-debian:bookworm-slim}`
- Data mount: `./data/agent-compose:/data`
- Extra runtime mount: `/var/run/docker.sock:/var/run/docker.sock`

Current key configuration for the shared playground `agent-compose-ui`
service:

- Listen port: `8000`
- Uses the independent `agent-compose-ui` image
- Reverse proxies daemon v2 Connect APIs, `/api/`, and Jupyter proxy routes
- Data mount: `./data:/data`, used for frontend service runtime data

The corresponding host data directory is:

- `/data/playground/data/agent-compose`

If agent-compose creates Docker runtime sandboxes through `/var/run/docker.sock`,
Docker bind mount sources must be host paths. In that case,
`DOCKER_HOST_SANDBOX_ROOT` must point to the actual host-side `sandboxes`
directory backing `SANDBOX_ROOT`.

Web/UI should no longer be validated as embedded static assets inside the daemon
container. The frontend may be served by nginx, a static file server, or an
independent container, and should reverse proxy to daemon v2 Connect APIs and
Jupyter proxy routes. The removed v1 control-plane API must not be used as a
deployment or verification requirement.

## Build Images

Run the current image build entry points from the code directory:

```bash
cd /data/code
task image:agent-compose-guest
task image:agent-compose
```

## Deploy To Shared Playground

Start or update daemon and independent frontend service:

```bash
docker compose -f /data/playground/docker-compose.yml up -d agent-compose agent-compose-ui
```

Force recreate containers after image updates:

```bash
docker compose -f /data/playground/docker-compose.yml up -d --force-recreate agent-compose agent-compose-ui
```

Check status:

```bash
docker compose -f /data/playground/docker-compose.yml ps
docker logs --tail 200 agent-compose
docker logs --tail 200 agent-compose-ui
```

## Basic Verification

### 1. Verify Daemon Status

```bash
curl -sS http://127.0.0.1:7410/api/version
```

If `agent-compose` CLI is already available locally, also verify:

```bash
agent-compose --host http://127.0.0.1:7410 status
```

### 2. Verify Independent Frontend Service Access

```bash
curl -i http://127.0.0.1:8000/ | head
curl -i http://127.0.0.1:8000/ui/ | head
```

### 3. Verify v2 ProjectService Main Path Access

An empty request should return validation issues, not 404:

```bash
curl -sS -X POST \
  http://127.0.0.1:7410/agentcompose.v2.ProjectService/ValidateProject \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{}'
```

### 4. Complete Project Smoke With CLI

Prepare a temporary compose file:

```bash
cat >/tmp/agent-compose-smoke.yml <<'YAML'
name: playground-smoke
agents:
  reviewer:
    provider: codex
    model: gpt-test
    image: debian:bookworm-slim
YAML
```

Run the main path:

```bash
agent-compose --host http://127.0.0.1:7410 -f /tmp/agent-compose-smoke.yml config --quiet
agent-compose --host http://127.0.0.1:7410 -f /tmp/agent-compose-smoke.yml up
agent-compose --host http://127.0.0.1:7410 -f /tmp/agent-compose-smoke.yml ps
agent-compose --host http://127.0.0.1:7410 -f /tmp/agent-compose-smoke.yml down
```

### 5. Verify Sandbox And Jupyter Through v2

Use the CLI `ps` output or the v2 `SandboxService.ListSandboxes` and
`SandboxService.GetSandbox` RPCs to obtain a sandbox ID and proxy entry. The
proxy path is normally `/jupyter/<sandbox_id>/lab`; the v2 response carries the
driver, runtime status, and notebook URL when the proxy is ready.

## Cold Start Characteristics

If any of these happened:

- a new `agent-compose-guest` image is used for the first time
- `/data/playground/data/agent-compose` was cleared
- `image-cache` or `boxlite` cache directories were deleted

then the first sandbox creation may be noticeably slower. This is usually normal
warmup and does not mean the RPC layer is stuck.

Important cache directories:

- `/data/playground/data/agent-compose/image-cache`
- `/data/playground/data/agent-compose/boxlite`

When debugging, first check:

```bash
docker logs -f agent-compose
```

Common progress logs include:

- `ensure session begin`
- `using materialized local image rootfs`
- `ensure session box ready`
- `starting box`
- `checking jupyter`
- `jupyter ready`

## Recommended Prewarm Steps

After clearing the data directory, prewarm after deployment:

1. Update and start the `agent-compose` container.
2. Create a temporary sandbox, for example `playground-prewarm`.
3. Poll `SandboxService.ListSandboxes` until the sandbox becomes `RUNNING`.
4. Start formal feature verification.

## Troubleshooting

### 1. Daemon Status Is Not Reachable

Check:

```bash
docker compose -f /data/playground/docker-compose.yml ps
docker logs --tail 200 agent-compose
docker logs --tail 200 agent-compose-ui
```

If the independent frontend cannot be opened, first verify the frontend service,
reverse proxy config, and connectivity from it to daemon
`http://127.0.0.1:7410` or the container network address. Do not use whether the
daemon container embeds `/agent-compose.html` as the signal for frontend
deployment success.

### 2. Sandbox Creation Fails Or Stays `PENDING`

Check:

```bash
docker logs --tail 200 agent-compose
```

Confirm first:

- which runtime driver the failing sandbox selected; for BoxLite or
  Microsandbox, whether `/dev/kvm` is available and usable, the deployment has
  the required privileged/KVM overlay, and native runtime artifacts are healthy
- whether `/var/run/docker.sock` is mounted correctly
- whether the image referenced by `DEFAULT_IMAGE` exists in host Docker or can
  be pulled
- whether this is only the first cold start rebuilding caches

### 3. Sandbox Proxy Returns 502 Or Notebook Is Not Reachable

Check:

- sandbox runtime status in `SandboxService.GetSandbox`
- `docker logs --tail 200 agent-compose`
- proxy / VM state files under the corresponding sandbox directory

Common file locations:

- `/data/playground/data/agent-compose/sandboxes/<year>/<month>/<day>/<sandbox_id>/metadata.json`
- `/data/playground/data/agent-compose/sandboxes/<year>/<month>/<day>/<sandbox_id>/vm/runtime.json`
- `/data/playground/data/agent-compose/sandboxes/<year>/<month>/<day>/<sandbox_id>/proxy/jupyter.json`

The date is the daemon's local calendar date when the sandbox was created.
Sandboxes created by older releases remain directly under `sandboxes/<sandbox_id>`.

### 4. Guest Image Update Does Not Take Effect

Rebuild images and force recreate containers:

```bash
cd /data/code
task image:agent-compose-guest
task image:agent-compose
docker compose -f /data/playground/docker-compose.yml up -d --force-recreate agent-compose agent-compose-ui
```
