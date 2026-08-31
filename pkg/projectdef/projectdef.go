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

func Parse(data []byte) (*ProjectSpec, error) { return compose.Parse(data) }

func ParseFile(path string) (*ProjectSpec, error) { return compose.ParseFile(path) }

func Normalize(spec *ProjectSpec, options NormalizeOptions) (*NormalizedProjectSpec, error) {
	return compose.Normalize(spec, options)
}

func NormalizeFile(path string) (*NormalizedProjectSpec, error) {
	return compose.NormalizeFile(path)
}

func ParseCanonicalJSON(data []byte) (*NormalizedProjectSpec, error) {
	return compose.ParseCanonicalJSON(data)
}

func IsProjectName(value string) bool { return compose.IsProjectName(value) }

const ProjectNamePattern = compose.ProjectNamePattern
