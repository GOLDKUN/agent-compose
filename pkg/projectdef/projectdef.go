// Package projectdef exposes the stable, definition-only project API.
//
// It intentionally contains no persistence or runtime-driver concerns. The
// aliases keep the public surface compatible with the compose schema while
// allowing callers to depend on a package whose contract is project
// definition parsing, normalization, and canonical serialization.
package projectdef

import "agent-compose/pkg/compose"

type ProjectSpec = compose.ProjectSpec
type NormalizeOptions = compose.NormalizeOptions
type NormalizedProjectSpec = compose.NormalizedProjectSpec
type ValidationError = compose.ValidationError


// Parse decodes a project definition from YAML or JSON.
func Parse(data []byte) (*ProjectSpec, error) { return compose.Parse(data) }

// ParseFile decodes a project definition from a file.
func ParseFile(path string) (*ProjectSpec, error) { return compose.ParseFile(path) }

// Normalize applies project defaults and validates definition-level rules.
func Normalize(spec *ProjectSpec, options NormalizeOptions) (*NormalizedProjectSpec, error) {
	return compose.Normalize(spec, options)
}

// NormalizeFile loads and normalizes a project definition file.
func NormalizeFile(path string) (*NormalizedProjectSpec, error) {
	return compose.NormalizeFile(path)
}

// Validate performs definition-level validation without runtime services.
func Validate(spec *ProjectSpec, options NormalizeOptions) error {
	_, err := compose.Normalize(spec, options)
	return err
}

// ParseCanonicalJSON decodes the canonical normalized representation.
func ParseCanonicalJSON(data []byte) (*NormalizedProjectSpec, error) {
	return compose.ParseCanonicalJSON(data)
}

// IsProjectName reports whether value follows the project naming contract.
func IsProjectName(value string) bool { return compose.IsProjectName(value) }

const ProjectNamePattern = compose.ProjectNamePattern
