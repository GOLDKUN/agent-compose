# Proto v2 string and enum boundaries

Proto v2 uses enums only for daemon-owned closed sets. A field remains a
string (or JSON string) when a provider, driver, plugin, model, or caller owns
its vocabulary, or when the daemon must accept and preserve unknown values.

Every closed-set enum has an `UNSPECIFIED = 0` value. Requests must reject
`UNSPECIFIED` and unknown numeric values when the field is required or present
as a filter. Responses may use `UNSPECIFIED` if persisted or adapter data
contains a value unknown to the current daemon. Clients must tolerate unknown
numeric enum values so adding a value remains a compatible protocol evolution.

## Audited fields

| Field | Owner and decision | Unknown-value behavior |
| --- | --- | --- |
| `Sandbox.status`, sandbox list/prune filters, `SandboxPruneCandidate.status` | Daemon-owned lifecycle; `SandboxStatus` | Request filters reject unknown values; responses project unknown internal values as `UNSPECIFIED` |
| `Sandbox.workspace_reclamation_state` | Daemon-owned cleanup lifecycle; `WorkspaceReclamationState` | Responses project unknown internal values as `UNSPECIFIED` |
| `SchedulerSpec.concurrency_policy` | Scheduler-owned `skip`/`parallel`; `SchedulerConcurrencyPolicy` | `UNSPECIFIED` means the compose default during input shaping |
| `SchedulerSpec.sandbox_policy`, `TriggerSpec.sandbox_policy` | Scheduler-owned `sticky`/`new`; shared `SchedulerSandboxPolicy` | `UNSPECIFIED` means compose default/inheritance behavior |
| `TriggerSpec.kind`, `SchedulerRun.trigger_kind` | Scheduler-owned trigger variants; shared `TriggerKind` | Unknown input values are not turned into a compose trigger; unknown persisted run values are projected as `UNSPECIFIED` |
| `VolumeMountSpec.type` | Daemon-owned `volume`/`bind`; `VolumeMountType` | `UNSPECIFIED` retains compose inference/default behavior |
| provider, model, and image names | Provider/runtime ecosystems; string | Accept and preserve extensions |
| `DriverSpec` selection | Daemon-compiled runtime drivers; `oneof config` with a matching name assertion | Unknown driver configurations are rejected |
| workspace provider and format | Workspace providers; string | Accept and preserve extensions |
| MCP server type and transport | MCP ecosystem; string | Accept and preserve extensions |
| capability protocol/runtime mode | Capability implementations; string | Accept and preserve extensions |
| event type/topic and metadata | Producers and callers; string/map | Accept and preserve extensions |
| payload, result, schema, and preset config JSON | Callers and integrations; JSON string | Validate JSON where required without constraining its vocabulary |

## Review rule

For every new string-like API field, record who owns the values, whether the
set is closed, whether unknown values must be preserved, whether adding values
is routine evolution, whether requests and responses share the set, and the
meaning of `UNSPECIFIED`. If a closed set cannot be demonstrated, use a string
and document that extensions are accepted.
