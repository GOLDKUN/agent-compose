package compose

import (
	"fmt"

	"agent-compose/pkg/sources"
)

// SourceCredentialMode identifies whether persisted source credentials still
// use authoring-time environment references or were resolved before transport.
type SourceCredentialMode uint8

const (
	// SourceCredentialsFromReferences requires password and token fields to use
	// environment references and resolves all credential references from
	// NormalizeOptions.Env. This is the default for compose authoring input.
	SourceCredentialsFromReferences SourceCredentialMode = iota
	// SourceCredentialsResolved accepts credential values that the CLI already
	// resolved and rejects remaining environment references.
	SourceCredentialsResolved
)

// WorkspaceCredentialMode is retained for source compatibility.
// Deprecated: use SourceCredentialMode.
type WorkspaceCredentialMode = SourceCredentialMode

const (
	// Deprecated: use SourceCredentialsFromReferences.
	WorkspaceCredentialsFromReferences = SourceCredentialsFromReferences
	// Deprecated: use SourceCredentialsResolved.
	WorkspaceCredentialsResolved = SourceCredentialsResolved
)

func normalizeSourceCredentials(path string, source sources.Source, options NormalizeOptions) (sources.Source, error) {
	switch options.SourceCredentials {
	case SourceCredentialsFromReferences:
		if err := validateSourceSecrets(path, source); err != nil {
			return sources.Source{}, err
		}
		return resolveSourceCredentialReferences(path, source, options)
	case SourceCredentialsResolved:
		if err := validateResolvedSourceCredentials(path, source); err != nil {
			return sources.Source{}, err
		}
		return source, nil
	default:
		return sources.Source{}, fmt.Errorf("unsupported source credential mode %d", options.SourceCredentials)
	}
}

func resolveSourceCredentialReferences(path string, source sources.Source, options NormalizeOptions) (sources.Source, error) {
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

func validateResolvedSourceCredentials(path string, source sources.Source) error {
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
				Message: "source credential must be resolved before submission",
			}
		}
	}
	return nil
}
