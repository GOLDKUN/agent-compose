# agent-compose Docker cron scheduler example

Languages: English | [中文](README.zh-CN.md)

This example shows a Docker-backed agent-compose project with a managed cron
scheduler.

It verifies the scheduler control-plane flow:

- parse a cron trigger from `agent-compose.yml`
- apply the project to the daemon
- create a managed project scheduler
- show the scheduler as enabled
- disable the scheduler with `agent-compose down`

The example does not require a model call for `config`, `up`, `ps`, or `down`.
The scheduled run itself still requires a working guest runtime and provider
authentication.

## Prerequisites

- Docker daemon is running.
- The `agent-compose` daemon is already running.
- The `agent-compose-guest:latest` image exists locally.

From the repository root, build the guest image if needed:

```bash
task image:agent-compose-guest
```

## Compose file

```yaml
name: docker-scheduler-cron

agents:
  reviewer:
    provider: codex
    image: agent-compose-guest:latest
    driver:
      docker: {}
    scheduler:
      enabled: true
      triggers:
        - name: hourly-review
          cron: "0 * * * *"
          prompt: "Review the current project state and summarize any important changes."
```

The trigger uses standard cron syntax. The expression below runs at the top of
every hour:

```yaml
cron: "0 * * * *"
```

## Run the example

From this directory:

```bash
agent-compose config
agent-compose up
agent-compose ps
agent-compose inspect project docker-scheduler-cron
agent-compose down
```

From the repository root without installing the binary:

```bash
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml config
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml up
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml ps
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml inspect project docker-scheduler-cron
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml down
```

Expected result:

- `config` prints the trigger as `kind: cron`.
- `up` creates one `scheduler` resource per configured scheduler.
- `ps` shows the scheduler as `enabled`.
- `inspect project` shows `scheduler_count: 1` and `trigger_count: 1`.
- `down` disables the managed scheduler.

## Making the trigger easier to observe

For a local demo where you want the scheduler to fire soon, use an interval
trigger instead of cron:

```yaml
scheduler:
  enabled: true
  triggers:
    - name: every-minute
      interval: 1m
      prompt: "Say hello from the interval trigger."
```

Use cron when you want calendar-based scheduling. Use interval when you want
short local feedback while testing.

## Verification output

Output from a local verification run.

### 1. Config normalization

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml config
name: docker-scheduler-cron
agents:
    - name: reviewer
      provider: codex
      image: agent-compose-guest:latest
      driver:
        name: docker
        docker: {}
      scheduler:
        enabled: true
        triggers:
            - name: hourly-review
              kind: cron
              cron: 0 * * * *
              prompt: Review the current project state and summarize any important changes.
```

### 2. Apply project

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml up
Project: docker-scheduler-cron
ID: project-docker-scheduler-cron-034aaf526f91
Revision: 1
Spec: sha256:609b72e32d33488851496faefccbe2e3487cf2247e5218dd5cde9ae31d57e964
Status: applied
Agents: 1
Schedulers: 1

ACTION   TYPE               NAME                                                                     ID
created  project            docker-scheduler-cron                                                    project-docker-scheduler-cron-034aaf526f91
created  project_revision   sha256:609b72e32d33488851496faefccbe2e3487cf2247e5218dd5cde9ae31d57e964  project-docker-scheduler-cron-034aaf526f91/1
created  project_agent      reviewer                                                                 agent-reviewer-4bff2fb6372a
created  agent_definition   reviewer                                                                 agent-reviewer-4bff2fb6372a
created  scheduler          reviewer                                                                 scheduler-reviewer-default-ed0b5bed0daa
```

### 3. Scheduler status

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml ps
AGENT     SCHEDULER  LATEST RUN  RUN STATUS  SESSION  DRIVER  IMAGE
reviewer  enabled    -           -           -        docker  agent-compose-guest:latest
```

### 4. Inspect project

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml inspect project docker-scheduler-cron
{
  "project": {
    "id": "project-docker-scheduler-cron-034aaf526f91",
    "name": "docker-scheduler-cron",
    "current_revision": 1,
    "agent_count": 1,
    "scheduler_count": 1
  },
  "agents": [
    {
      "agent_name": "reviewer",
      "provider": "codex",
      "image": "agent-compose-guest:latest",
      "driver": "docker",
      "scheduler_enabled": true
    }
  ],
  "schedulers": [
    { "agent_name": "reviewer", "enabled": true, "trigger_count": 1 }
  ]
}
```

### 5. Disable scheduler

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-cron/agent-compose.yml down
Project: docker-scheduler-cron
ID: project-docker-scheduler-cron-034aaf526f91
Status: down
Failed session stops: 0

ACTION   TYPE               NAME      ID                                       MESSAGE
updated  scheduler          reviewer  scheduler-reviewer-default-ed0b5bed0daa  disabled by project down
```
