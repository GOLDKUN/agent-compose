# agent-compose Docker timeout scheduler example

Languages: English | [中文](README.zh-CN.md)

This example runs an end-to-end scheduled agent flow with the Docker runtime.

It verifies that agent-compose can:

- parse a timeout trigger from `agent-compose.yml`
- apply the project to the daemon
- create a managed scheduler
- let the scheduler fire automatically
- start a Docker-backed agent runtime session
- run the configured agent prompt
- persist the successful project run and logs
- disable the scheduler with `agent-compose down`

## Prerequisites

- Docker daemon is running.
- The `agent-compose` daemon is already running.
- The `agent-compose-guest:latest` image exists locally.
- The guest image has working Codex credentials or API access.

From the repository root, build the guest image if needed:

```bash
task image:agent-compose-guest
```

## Compose file

```yaml
name: docker-scheduler-timeout

agents:
  reviewer:
    provider: codex
    image: agent-compose-guest:latest
    driver:
      docker: {}
    scheduler:
      enabled: true
      triggers:
        - name: run-once-after-15-seconds
          timeout: 15s
          prompt: "Reply with exactly: timeout scheduler ok"
```

The `timeout: 15s` trigger is intentionally short so the full flow can be
tested quickly.

## Run the example

From this directory:

```bash
agent-compose config
agent-compose up
sleep 35
agent-compose ps
agent-compose inspect run <run-id>
agent-compose logs --run <run-id>
agent-compose down
```

From the repository root without installing the binary:

```bash
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml config
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml up
sleep 35
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml ps
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml inspect run <run-id>
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml logs --run <run-id>
go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml down
```

Replace `<run-id>` with the run id shown in the `ps` output.

Expected result:

- `config` prints the trigger as `kind: timeout`.
- `up` creates or updates one `scheduler` resource per configured scheduler.
- After the timeout fires once, `ps` shows a scheduler-created run.
- `inspect run <run-id>` shows `source: scheduler`, `status: succeeded`, `driver: docker`, and output from the agent.
- `logs --run <run-id>` prints the agent output.
- `down` disables the managed scheduler.

## Verification output

Output from a local verification run. The run id below is from that run; yours
will differ.

### 1. Config normalization

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml config
name: docker-scheduler-timeout
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
            - name: run-once-after-15-seconds
              kind: timeout
              timeout: 15s
              prompt: 'Reply with exactly: timeout scheduler ok'
```

### 2. Apply project

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml up
Project: docker-scheduler-timeout
ID: project-docker-scheduler-timeout-3a00cafbae27
Revision: 1
Spec: sha256:3b8a286e2cf7df774375a5eeeef1a87f9fad75921bde212e539a15c9081b196f
Status: applied
Agents: 1
Schedulers: 1

ACTION   TYPE               NAME                                                                     ID
created  project            docker-scheduler-timeout                                                 project-docker-scheduler-timeout-3a00cafbae27
created  project_revision   sha256:3b8a286e2cf7df774375a5eeeef1a87f9fad75921bde212e539a15c9081b196f  project-docker-scheduler-timeout-3a00cafbae27/1
created  project_agent      reviewer                                                                 agent-reviewer-a0befcb745b8
created  agent_definition   reviewer                                                                 agent-reviewer-a0befcb745b8
created  scheduler          reviewer                                                                 scheduler-reviewer-default-181247660dc1
```

### 3. Successful scheduled run

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml ps
AGENT     SCHEDULER  LATEST RUN                 RUN STATUS  SESSION  DRIVER  IMAGE
reviewer  enabled    run-reviewer-28c0ef985c8d  succeeded   -        docker  agent-compose-guest:latest
```

### 4. Inspect successful run

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml inspect run run-reviewer-28c0ef985c8d
{
  "run_id": "run-reviewer-28c0ef985c8d",
  "project_name": "docker-scheduler-timeout",
  "agent_name": "reviewer",
  "source": "scheduler",
  "status": "succeeded",
  "session_id": "23a1ede4-3325-470d-99db-377e3296e7a2",
  "exit_code": 0,
  "duration_ms": 10917,
  "prompt": "Reply with exactly: timeout scheduler ok",
  "output": "timeout scheduler ok",
  "result_json": "{\"agent\":\"codex\",\"exitCode\":0,\"stopReason\":\"completed\",\"success\":true}",
  "driver": "docker",
  "image_ref": "agent-compose-guest:latest"
}
```

### 5. Run logs

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml logs --run run-reviewer-28c0ef985c8d
timeout scheduler ok
```

### 6. Disable scheduler

```console
$ go run ./cmd/agent-compose --file examples/agent-compose/docker-scheduler-timeout/agent-compose.yml down
Project: docker-scheduler-timeout
ID: project-docker-scheduler-timeout-3a00cafbae27
Status: down
Failed session stops: 0

ACTION   TYPE               NAME      ID                                       MESSAGE
updated  scheduler          reviewer  scheduler-reviewer-default-181247660dc1  disabled by project down
```
