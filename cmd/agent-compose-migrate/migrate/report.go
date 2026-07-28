package migrate

import (
	"fmt"
	"strings"
)

type Report struct {
	Source            string   `json:"source"`
	Target            string   `json:"target"`
	SourceFingerprint string   `json:"source_fingerprint,omitempty"`
	SourceVersion     int64    `json:"source_version,omitempty"`
	TargetVersion     int64    `json:"target_version,omitempty"`
	Stage             string   `json:"stage"`
	CopiedFiles       int      `json:"copied_files,omitempty"`
	CopiedBytes       int64    `json:"copied_bytes,omitempty"`
	CheckedFiles      int      `json:"checked_files,omitempty"`
	CheckedBytes      int64    `json:"checked_bytes,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	Error             string   `json:"error,omitempty"`
	DryRun            bool     `json:"dry_run,omitempty"`
	InPlace           bool     `json:"in_place,omitempty"`
	Backup            string   `json:"backup,omitempty"`
}

func (r Report) Text() string {
	text := ""
	if r.Error != "" {
		text = fmt.Sprintf("legacy migration %s: %s", r.Stage, r.Error)
	} else if r.DryRun {
		text = fmt.Sprintf("legacy migration dry run: source schema version %d is eligible", r.SourceVersion)
	} else if r.InPlace {
		text = fmt.Sprintf("legacy migration complete: schema v%d migrated in place at %s; backup retained at %s", r.TargetVersion, r.Target, r.Backup)
	} else {
		text = fmt.Sprintf("legacy migration complete: schema v%d, %d files (%d bytes) copied to %s", r.TargetVersion, r.CopiedFiles, r.CopiedBytes, r.Target)
	}
	for _, warning := range r.Warnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			text += "\nwarning: " + warning
		}
	}
	return text
}
