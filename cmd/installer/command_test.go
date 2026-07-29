package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/chaitin/agent-compose/cmd/installer/internal/core"
)

type testTTY struct {
	input  io.Reader
	output bytes.Buffer
}

func (t *testTTY) Read(data []byte) (int, error)  { return t.input.Read(data) }
func (t *testTTY) Write(data []byte) (int, error) { return t.output.Write(data) }

func TestRootHelpDocumentsOperationsAndDefaultDirectory(t *testing.T) {
	var output bytes.Buffer
	command := newRootCommand(&output, &output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"install", "upgrade", "uninstall", "/opt/agent-compose", "--backend-image", "--frontend-image", "--guest-image"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help missing %q:\n%s", expected, output.String())
		}
	}
}

func TestRootRejectsExplicitEmptyImage(t *testing.T) {
	command := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs([]string{"install", "--backend-image", "", "--yes"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "backend image must be a non-empty image reference") {
		t.Fatalf("Execute error = %v", err)
	}
}

func TestRootRejectsInteractiveModeWithoutTTY(t *testing.T) {
	command := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs(nil)
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "requires a TTY") {
		t.Fatalf("Execute error = %v", err)
	}
}

func TestTruthy(t *testing.T) {
	if !truthy("true") || !truthy("1") || !truthy("yes") || truthy("") {
		t.Fatal("unexpected truthy parsing")
	}
}

func TestConfirmOperationUsesControllingTTYForPromptAndAnswer(t *testing.T) {
	tty := &testTTY{input: strings.NewReader("yes\n")}
	options := core.DefaultOptions()
	options.InstallDir = "/opt/test-agent-compose"
	options.Purge = true

	confirmed, err := confirmOperationOnTTY(core.OperationUninstall, options, tty)
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("yes answer was not accepted")
	}
	want := "uninstall agent-compose in /opt/test-agent-compose and permanently delete its configuration and data? [y/N] "
	if tty.output.String() != want {
		t.Fatalf("TTY prompt = %q, want %q", tty.output.String(), want)
	}
}
