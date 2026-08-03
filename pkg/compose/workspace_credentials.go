package compose

import (
	"fmt"

	"agent-compose/pkg/sources"
)

// WorkspaceCredentialMode identifies whether workspace credentials still use
// authoring-time environment references or have already been resolved before
// transport to the daemon.
type WorkspaceCredentialMode uint8

const (
	// WorkspaceCredentialsFromReferences requires password and token fields to
	// use environment references and resolves all credential references from
	// NormalizeOptions.Env. This is the default for compose authoring input.
	WorkspaceCredentialsFromReferences WorkspaceCredentialMode = iota
	// WorkspaceCredentialsResolved accepts credential values that the CLI has
	// already resolved and rejects remaining environment references.
	WorkspaceCredentialsResolved
)

func normalizeWorkspaceCredentials(path string, source sources.Source, options NormalizeOptions) (sources.Source, error) {
	switch options.WorkspaceCredentials {
	case WorkspaceCredentialsFromReferences:
		if err := validateSourceSecrets(path, source); err != nil {
			return sources.Source{}, err
		}
		return resolveWorkspaceCredentialReferences(path, source, options)
	case WorkspaceCredentialsResolved:
		if err := validateResolvedWorkspaceCredentials(path, source); err != nil {
			return sources.Source{}, err
		}
		return source, nil
	default:
		return sources.Source{}, fmt.Errorf("unsupported workspace credential mode %d", options.WorkspaceCredentials)
	}
}

func resolveWorkspaceCredentialReferences(path string, source sources.Source, options NormalizeOptions) (sources.Source, error) {
	var err error
	source.Username, err = interpolateEnvValue(path+".username", source.Username, options)
	if err != nil {
		return sources.Source{}, err
	}
	source.Password, err = interpolateEnvValue(path+".password", source.Password, options)
	if err != nil {
		return sources.Source{}, err
	}
	source.Token, err = interpolateEnvValue(path+".token", source.Token, options)
	if err != nil {
		return sources.Source{}, err
	}
	return source.Normalized(), nil
}

func validateResolvedWorkspaceCredentials(path string, source sources.Source) error {
	credentials := []struct {
		name  string
		value string
	}{
		{name: "username", value: source.Username},
		{name: "password", value: source.Password},
		{name: "token", value: source.Token},
	}
	for _, credential := range credentials {
		if envReferencePattern.MatchString(credential.value) {
			return &ValidationError{
				Path:    path + "." + credential.name,
				Message: "workspace credential must be resolved before submission",
			}
		}
	}
	return nil
}
