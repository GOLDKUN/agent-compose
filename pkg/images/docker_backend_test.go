package images

import "testing"

func TestCloneBuildArgsNormalizesKeysAndCopiesValues(t *testing.T) {
	tests := []struct {
		name string
		args map[string]string
	}{
		{name: "nil"},
		{name: "empty", args: map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cloneBuildArgs(test.args); got != nil {
				t.Fatalf("cloneBuildArgs(%#v) = %#v, want nil", test.args, got)
			}
		})
	}

	args := map[string]string{
		"  ":      "discarded",
		" plain ": " unchanged ",
	}
	got := cloneBuildArgs(args)
	if len(got) != 1 || got["plain"] != " unchanged " {
		t.Fatalf("cloneBuildArgs(%#v) = %#v", args, got)
	}

	args[" plain "] = "mutated"
	args["new"] = "caller"
	if len(got) != 1 || got["plain"] != " unchanged " {
		t.Fatalf("cloned build args changed with caller map: %#v", got)
	}
	got["plain"] = "normalized mutation"
	if args[" plain "] != "mutated" {
		t.Fatalf("caller build args changed with cloned map: %#v", args)
	}
}

func TestCloneBuildArgsCollapsesKeysEqualAfterTrimming(t *testing.T) {
	got := cloneBuildArgs(map[string]string{
		"ARG":   "first",
		" ARG ": "second",
	})
	if len(got) != 1 {
		t.Fatalf("cloneBuildArgs duplicate trimmed keys = %#v, want one entry", got)
	}
	if value := got["ARG"]; value != "first" && value != "second" {
		t.Fatalf("cloneBuildArgs duplicate trimmed key value = %q, want an original value", value)
	}
}
