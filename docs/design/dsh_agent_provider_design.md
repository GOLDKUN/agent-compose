# DSH agent provider design

## 1. Overview

`dsh` (DeepSeek Harness) is a Cordis-based agent runtime, added to agent-compose as a sixth provider alongside `codex`, `claude`, `gemini`, `opencode`, `pi`. Unlike the others, `dsh` is not a single CLI binary with flags — it boots a *profile*: an ordered stack of plugin-bundle patch layers. agent-compose ships its own profile (`assets/.dsh/profiles/agent-compose/`) rather than passing flags to a generic binary.

## 2. Composition model

The profile is a static overlay on top of the `@deepseek-ai/dsh-base` bundle:

- `cordis.patch.yml` — patches individual plugin rows from `dsh-base` (model/provider selection, session persistence root, skill filesystem scope, credential/settings sources) and inserts one agent-compose-owned plugin (`agent-compose-runner`, see §3.1).
- `runner.js` — the inserted plugin's implementation.
- `package.json` — declares the profile's bundle (`dsh-base`); no other dependencies are needed because everything `runner.js` imports (`dsh-llm`, `dsh-session`, `dsh-mcp-client`, …) resolves transitively through the globally-installed `@deepseek-ai/dsh` package's own `node_modules`.

This file ships as a repo asset (`assets/.dsh/...`), baked into the guest image at `/root/.dsh` — not an npm package, and not rebuilt per run. Every mutable, per-run value (model, credentials, skills, MCP servers, prompt text, session id) has to flow in through the **spawn-time environment** that `runtime/javascript/src/runners/dsh.ts` sets when it `spawn()`s `dsh --profile agent-compose` (§3.2), because the profile itself is fixed at image-build time.

## 3. Runtime driving

### 3.1 `agent-compose-runner` plugin

`runner.js` is inserted into the profile via `cordis.patch.yml`'s `insert:` list, injecting `agents`, `sessions`, and `agentDefaultModel`. It is modeled on `@deepseek-ai/dsh-headless`'s one-shot driver (create, followup, whenIdle, flush, exit) and `dsh-cc-tui`'s create-vs-resume/event-subscription pattern, but reuses neither directly: headless has no resume or event stream, and cc-tui is interactive.

### 3.2 Parameterization via spawn-time environment

`cordis.patch.yml` reads every per-run value with `!!js process.env.X` (a `new Function('ctx','expr','with(ctx){return eval(expr)}')` sandbox with no `require`). That constrains parameterization to environment variables — no temp-file indirection, since the eval sandbox can't `readFileSync`. A `spawn()`-passed env object survives embedded newlines untouched (no shell involved), which is what lets `DSH_SYSTEM_CONTEXT` carry multi-line system-prompt text safely.

### 3.3 Create vs. resume

`DSH_RESUME=1` plus `DSH_SESSION_ID` selects `agents.resume()`; otherwise `agents.create()` with a host-generated `session-<uuid>`. A resume miss is deliberately uncaught — falling back to `create()` would silently drop the caller's history, so it fails loud instead.

### 3.4 Event streaming protocol

`runner.js` subscribes to `ctx.on('session/event', ...)` and writes each event to stdout as `{"type":"session_event","sessionId":...,"event":...}\n`. `dsh.ts` parses this line-by-line, cross-checking `sessionId` when present and mapping `assistant/chunk` → transcript text, `assistant/message` → final text, `turn/end`'s `reason.kind` → `stopReason` (surfacing `reason.error` as a thrown error for `kind: "error"`).

### 3.5 Environment variable reference

| Variable | Set by | Purpose |
| --- | --- | --- |
| `DSH_MODEL` | `dsh.ts` | Model name (provider routing is resolved host-side; only the model literal crosses) |
| `DSH_REASONING_EFFORT` | `dsh.ts` | agent-compose's 5-level `effort` collapsed to DSH's 2-level `high`/`max` (§6 has no equivalent collapse — this is the reasoning-effort case) |
| `DSH_PERMISSION_MODE` | facade config + `dsh.ts` | Always `danger-full-access`; guest sandboxing is the agent-compose sandbox, not a nested DSH one (§5.3/§5.5) |
| `DSH_SESSION_ROOT`, `DSH_SESSION_ID`, `DSH_RESUME` | `dsh.ts` | Session persistence and resume (§3.3) |
| `DSH_PROMPT_FILE` | `dsh.ts` | Path to the prompt text file `runner.js` reads |
| `DSH_SYSTEM_CONTEXT` | `dsh.ts` | Persona text, read directly by `cordis.patch.yml`'s `system-prompt` row (§3.2) |
| `DSH_SKILL_DIRS` | `dsh.ts` | Colon-joined resolved skill directories; consumed by the `skill-filesystem` row's `customSkillDirs` (§5.1) |
| `DSH_MCP_SERVERS` | `dsh.ts` | JSON array of per-server `dsh-mcp-client` configs; consumed by `runner.js` (§6) |
| `LLM_API_KEY`, `LLM_API_ENDPOINT` | facade config | Consumed by the `llm-deepseek` row (§4) |

## 4. LLM facade routing

### 4.1 Facade token and wire protocol

`EnsureDshFacadeConfig` (`pkg/llms/dsh_facade.go`) always issues a chat-completions facade token and points the guest at `/llm/openai/v1`, regardless of the resolved upstream provider's own protocol — the facade bridges the difference, so DSH's own upstream protocol is irrelevant to the guest. Model selection is `<llm-provider-id>/<model-name>` (`SplitDshModel`), the same shape Pi and OpenCode use.

### 4.2 `llm-deepseek` route

`cordis.patch.yml`'s `llm-deepseek` row registers a single route, `deepseek-official`, reading its API key/base URL/reasoning effort from the spawn environment. `agent-default-model` selects `deepseek-official` + `DSH_MODEL`.

## 5. Security and isolation

### 5.1 Skill tenant isolation

`skill-filesystem`'s `includeDefaultRoots: false` plus `customSkillDirs` from `DSH_SKILL_DIRS` means an agent only ever sees the skill directories agent-compose resolved for it, never a shared `~/.agents/skills` tree.

### 5.2 Model/provider resolution

Resolution mirrors Pi's (`resolveDshFacadeTarget` mirrors `resolvePiFacadeTarget`'s branch structure: configured provider id → family → custom OpenAI), minus an Anthropic-family branch — `llm-deepseek` always speaks chat completions, so there is nothing to mirror there.

### 5.3 Sandbox policy / permission mode

No approval or sandbox-policy overrides exist in the patch: `dsh-base`'s own rows key off `DSH_PERMISSION_MODE`, which agent-compose always sets to `danger-full-access`.

### 5.4 Credential source

`credentials` and `settings` rows are disabled, so `$DSH_HOME/settings.yaml` can't override `llm-deepseek`'s API key or base URL at runtime, and local credential discovery is off. The run-scoped facade token (§4.1) is the only LLM credential source.

### 5.5 Guest sandboxing boundary

`danger-full-access` (§5.3) is safe because DSH's own sandbox-policy layer is not the isolation boundary — the agent-compose sandbox (container/VM) is. A nested provider-side sandbox would be redundant.

## 6. MCP support

`@deepseek-ai/dsh-mcp-client` is an upstream DSH package — DSH already implements the MCP client protocol; agent-compose only wires its own generic `mcp_servers` config into it. One `dsh-mcp-client` plugin instance handles exactly one MCP server; there is no single "MCP" plugin that takes a server list.

Because `cordis.patch.yml` is static and the server list is a dynamic 0..N value known only at run time (§2), the wiring lives in `runner.js` rather than as YAML rows: `registerMcpServers()` parses `DSH_MCP_SERVERS` (§3.5) and calls `ctx.plugin(dshMcpClient, config)` once per server before the agent's first turn. `ctx.plugin()`'s returned Fiber settles once that server's plugin has finished loading, so `await`ing all of them guarantees every server's tools are registered before `agent.followup()` fires.

`dsh.ts`'s `toDshMcpServers()` maps agent-compose's generic `RuntimeMCPServer` (`type: "local"|"remote"`) onto `dsh-mcp-client`'s shape (`transport: "stdio"|"streamable-http"`): `local` → `stdio`, `remote`+`http` → `streamable-http`. `remote`+`sse` has no `dsh-mcp-client` equivalent and is rejected fail-fast, naming the offending server. Server names are sanitized to `dsh-mcp-client`'s `[A-Za-z0-9_-]{1,32}` requirement and suffixed with a deterministic hash of the raw name, since agent-compose's own name validation doesn't guarantee that charset.

**Known limitation:** `dsh-mcp-client`'s `StdioClientTransport` construction doesn't pass a `stderr` option, so the MCP SDK defaults the spawned server's stderr to `'inherit'` — it lands directly in `dsh`'s own stderr, indistinguishable from DSH's own diagnostics. `dsh.ts`'s `child.stderr` handler treats all of `dsh`'s stderr as transcript text, so any stdio MCP server that logs to its own stderr on startup (a common convention) will have that text appear in the agent's transcript. There is no `dsh-mcp-client` config option to suppress this today; fixing it requires an upstream change.
