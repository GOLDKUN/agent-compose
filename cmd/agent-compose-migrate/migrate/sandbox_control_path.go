package migrate

import (
	"path/filepath"
	"strings"
	"time"
)

func isSandboxMetadataPath(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	return isFlatSandboxControlPath(parts, "metadata.json") ||
		isPartitionedSandboxControlPath(parts, "metadata.json")
}

func isSandboxMountManifestPath(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	return isFlatSandboxControlPath(parts, "vm", "mount-manifest.json") ||
		isPartitionedSandboxControlPath(parts, "vm", "mount-manifest.json")
}

func isFlatSandboxControlPath(parts []string, suffix ...string) bool {
	if len(parts) != 2+len(suffix) || !isSandboxRootName(parts[0]) || parts[1] == ".lifecycle" {
		return false
	}
	return equalPathParts(parts[2:], suffix)
}

func isPartitionedSandboxControlPath(parts []string, suffix ...string) bool {
	if len(parts) != 5+len(suffix) || !isSandboxRootName(parts[0]) || !validSandboxDatePartition(parts[1:4]) {
		return false
	}
	return equalPathParts(parts[5:], suffix)
}

func isSandboxRootName(name string) bool {
	return name == "sessions" || name == "sandboxes"
}

func validSandboxDatePartition(parts []string) bool {
	if len(parts) != 3 || !validSandboxDatePart(parts[0], 4) || !validSandboxDatePart(parts[1], 2) || !validSandboxDatePart(parts[2], 2) {
		return false
	}
	_, err := time.Parse("2006/01/02", strings.Join(parts, "/"))
	return err == nil
}

func validSandboxDatePart(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func equalPathParts(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
