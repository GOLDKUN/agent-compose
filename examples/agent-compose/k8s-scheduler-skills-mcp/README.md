# agent-compose k8s driver: scheduler, skills, and MCP

Languages: English | [中文](README.zh-CN.md)

Three `driver: k8s` agents in one project, each exercising a different
guest-sync path that has no shared filesystem to fall back on (see
`docs/design/k8s_pod_runtime_driver_design.md` §2.1):

- `scheduled`: a cron trigger, verifying the scheduler creates and runs a k8s
  sandbox on schedule.
- `skilled`: a git-sourced skill, verifying the daemon resolves it and pushes
  it into the guest Pod via exec.
- `mcp-enabled`: a project-level MCP server reference, verifying the managed
  MCP config block is pushed into the guest Pod's `~/.codex/config.toml`.

Verified against a live k3d cluster with this branch's `k8s` driver: the
scheduler fired and released its sandbox, the `pdf` skill landed in
`~/.agents/skills` and `~/.claude/skills` in the guest Pod, and the
`filesystem` MCP server started successfully over stdio inside the Pod.

## Prerequisites

- An `agent-compose` daemon running with `driver: k8s` (see
  `charts/agent-compose`), pointed at a cluster where `agent-compose-guest:latest`
  is available to the nodes.
- The daemon's guest image needs `git` for the `skilled` agent, and Node.js
  (already in `agent-compose-guest:latest`) for the MCP server.

## Apply

```bash
agent-compose --host <daemon-http-endpoint> up
agent-compose --host <daemon-http-endpoint> scheduler trigger scheduled every-minute
agent-compose --host <daemon-http-endpoint> run skilled --command true --keep-running
agent-compose --host <daemon-http-endpoint> run mcp-enabled --command true --keep-running
```
