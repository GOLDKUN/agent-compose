package migrate

import (
	"strings"
	"testing"
)

func TestReportTextIncludesWarningsForEverySuccessfulMode(t *testing.T) {
	if got := (Report{Stage: "validate", Error: "bad source"}).Text(); got != "legacy migration validate: bad source" {
		t.Fatalf("error report text = %q", got)
	}
	if got := (Report{DryRun: true, SourceVersion: 4}).Text(); got != "legacy migration dry run: source schema version 4 is eligible" {
		t.Fatalf("dry-run report text = %q", got)
	}
	if got := (Report{TargetVersion: currentSchemaVersion, CopiedFiles: 2, CopiedBytes: 9, Target: "/target"}).Text(); got != "legacy migration complete: schema v8, 2 files (9 bytes) copied to /target" {
		t.Fatalf("complete report text = %q", got)
	}
	warningReport := Report{TargetVersion: currentSchemaVersion, CopiedFiles: 2, Target: "/target", Warnings: []string{"unresolved scheduler link", "  external path retained  ", ""}}
	if got, want := warningReport.Text(), "legacy migration complete: schema v8, 2 files (0 bytes) copied to /target\nwarning: unresolved scheduler link\nwarning: external path retained"; got != want {
		t.Fatalf("warning report text = %q, want %q", got, want)
	}
	for name, report := range map[string]Report{
		"dry run":  {DryRun: true, SourceVersion: 4, Warnings: []string{"dry-run warning"}},
		"in place": {InPlace: true, TargetVersion: currentSchemaVersion, Target: "/data", Backup: "/data/backup", Warnings: []string{"in-place warning"}},
	} {
		t.Run(name+" warnings", func(t *testing.T) {
			if got := report.Text(); !strings.Contains(got, "\nwarning: "+report.Warnings[0]) {
				t.Fatalf("report text omitted warning: %q", got)
			}
		})
	}
}
