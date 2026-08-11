# Stable YAML compatibility fixtures

The public authoring schema is backward compatible from `v2608.1.0`, whose
release commit is recorded in `contract-v2608.1.0.json`.

The contract is cumulative. Removing or changing an existing entry fails
`TestStableYAMLContract`. A reviewed optional addition can be appended with:

```bash
UPDATE_STABLE_YAML_CONTRACT=1 go test ./pkg/compose -run '^TestStableYAMLContract$'
```

The update path only merges additions; it cannot remove or rewrite an existing
stable entry. Add a parse-and-normalize fixture when a new field has custom YAML
forms, validation, interpolation, or default semantics that the structural
contract cannot describe. Fixtures must be deterministic and must not use the
network or developer-machine state.
