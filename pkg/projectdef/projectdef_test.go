package projectdef

import "testing"

func TestParseNormalizeAndCanonicalRoundTrip(t *testing.T) {
	spec, err := Parse([]byte("name: demo\nagents:\n  worker:\n    provider: test\n"))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := Normalize(spec, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := normalized.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseCanonicalJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "demo" || len(decoded.Agents) != 1 || decoded.Agents[0].Name != "worker" {
		t.Fatalf("unexpected normalized project: %#v", decoded)
	}
}

func TestProjectNameContract(t *testing.T) {
	if !IsProjectName("valid_name-1") {
		t.Fatal("expected valid project name")
	}
	if IsProjectName("Invalid Name") {
		t.Fatal("expected invalid project name")
	}
}

func TestValidateRejectsInvalidDefinition(t *testing.T) {
	if err := Validate(&ProjectSpec{Name: "Invalid Name"}, NormalizeOptions{}); err == nil {
		t.Fatal("expected invalid project name to be rejected")
	}
}

func TestValidateDoesNotResolveRuntimeSources(t *testing.T) {
	spec := &ProjectSpec{Name: "demo"}
	err := Validate(spec, NormalizeOptions{ResolveScriptURLs: true})
	if err != nil {
		t.Fatal(err)
	}
}
