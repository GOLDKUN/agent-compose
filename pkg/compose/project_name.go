package compose

import "strings"

// ProjectNamePattern is the Docker Compose-compatible project name contract.
const ProjectNamePattern = `^[a-z0-9][a-z0-9_-]*$`

// IsProjectName reports whether value follows the Docker Compose project-name
// character contract. Other project-scoped resource names intentionally keep
// the stricter stable-identifier contract enforced in pkg/projects.
func IsProjectName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case i == 0 && r >= 'a' && r <= 'z':
		case i == 0 && r >= '0' && r <= '9':
		case i > 0 && r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && (r == '-' || r == '_'):
		default:
			return false
		}
	}
	return true
}
