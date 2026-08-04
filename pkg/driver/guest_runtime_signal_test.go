package driver

import (
	"errors"
	"reflect"
	"testing"
)

const guestRuntimeSignalTestID = "9a497cd5-d990-46db-a01c-13bbed138c30"

func TestGuestRuntimeControlEnvCopiesAndReplacesPrivateValues(t *testing.T) {
	input := map[string]string{
		"USER_VALUE":          "preserved",
		executionIDEnv:        "caller-id",
		executionReadyFileEnv: "/caller/path",
	}
	got := GuestRuntimeControlEnv(input, guestRuntimeSignalTestID)
	want := map[string]string{
		"USER_VALUE":          "preserved",
		executionIDEnv:        guestRuntimeSignalTestID,
		executionReadyFileEnv: guestRuntimeReadyRoot + "/" + guestRuntimeSignalTestID,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GuestRuntimeControlEnv() = %#v, want %#v", got, want)
	}
	got["USER_VALUE"] = "changed"
	if input["USER_VALUE"] != "preserved" {
		t.Fatal("GuestRuntimeControlEnv() mutated caller-owned input")
	}
}

func TestGuestRuntimeSignalCommandTargetsOneReadyFile(t *testing.T) {
	command, err := guestRuntimeSignalCommand(guestRuntimeSignalTestID, RuntimeSignalTerminate)
	if err != nil {
		t.Fatalf("guestRuntimeSignalCommand() error = %v", err)
	}
	if len(command) != 7 || command[0] != "sh" || command[4] != guestRuntimeReadyRoot+"/"+guestRuntimeSignalTestID || command[5] != guestRuntimeSignalTestID || command[6] != "TERM" {
		t.Fatalf("guestRuntimeSignalCommand() = %#v", command)
	}
	if _, err := guestRuntimeSignalCommand("../escape", RuntimeSignalTerminate); err == nil {
		t.Fatal("guestRuntimeSignalCommand() accepted a path-like execution ID")
	}
	if _, err := guestRuntimeSignalCommand(guestRuntimeSignalTestID, RuntimeSignal("unknown")); err == nil {
		t.Fatal("guestRuntimeSignalCommand() accepted an unknown signal")
	}
}

func TestGuestRuntimeSignalExitErrorClassifiesRetryAndGone(t *testing.T) {
	if err := guestRuntimeSignalExitError(0); err != nil {
		t.Fatalf("guestRuntimeSignalExitError(0) = %v", err)
	}
	if err := guestRuntimeSignalExitError(75); !errors.Is(err, ErrGuestRuntimeNotReady) {
		t.Fatalf("guestRuntimeSignalExitError(75) = %v", err)
	}
	if err := guestRuntimeSignalExitError(76); !errors.Is(err, ErrGuestRuntimeGone) {
		t.Fatalf("guestRuntimeSignalExitError(76) = %v", err)
	}
	if err := guestRuntimeSignalExitError(2); err == nil {
		t.Fatal("guestRuntimeSignalExitError(2) returned nil")
	}
}
