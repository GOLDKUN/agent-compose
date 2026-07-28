# Project spec hash contract

`ValidateProjectRequest.submitted_spec_hash` and
`ApplyProjectRequest.submitted_spec_hash` are optional integrity checks for the
spec carried by that request. The daemon normalizes the submitted `ProjectSpec`,
computes its hash, and compares that result with the non-empty request value. A
mismatch is returned as a validation issue at `submitted_spec_hash`. An empty
value skips the comparison.

This check never reads the persisted project's current revision or spec hash.
It is therefore not an optimistic-concurrency precondition: a later valid apply
may replace an earlier apply to the same project.

## Canonical form and algorithm

The Go `pkg/compose` package owns normalization and the hash contract. After
normalization, `NormalizedProjectSpec.MarshalCanonicalJSON(false)` produces the
canonical bytes, including secrets and using the package's stable ordered wire
shape. `NormalizedProjectSpec.Hash` computes SHA-256 over those bytes and
encodes the result as lowercase hexadecimal prefixed with `sha256:`.

Clients do not need to reproduce this algorithm to validate a project: they may
leave `submitted_spec_hash` empty and use `ValidateProjectResponse.spec_hash`.
The CLI computes the same normalized hash locally and sends it on apply so that
client/server normalization drift is reported consistently.

## Future concurrency preconditions

Optimistic concurrency can be added compatibly with a new optional
`expected_revision` or `expected_current_spec_hash` field. Such a field must be
checked against persisted state, including for dry runs and unchanged applies,
and should fail with a transport-level precondition error rather than a
submitted-spec validation issue. If both future preconditions are provided,
both must match (logical AND). The existing `submitted_spec_hash` check remains
independent and keeps its request-integrity semantics.
