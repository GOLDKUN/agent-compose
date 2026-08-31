package projects

import (
	"slices"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/projectdef"
)

func SandboxEnvItemsFromCompose(values map[string]projectdef.EnvVarSpec) []domain.SandboxEnvVar {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	items := make([]domain.SandboxEnvVar, 0, len(values))
	for _, name := range names {
		value := values[name]
		items = append(items, domain.SandboxEnvVar{Name: name, Value: value.Value, Secret: value.Secret})
	}
	return items
}
