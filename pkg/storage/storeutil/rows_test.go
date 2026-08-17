package storeutil

import (
	"errors"
	"fmt"
	"testing"
)

func TestReportCloseSurfacesCloseFailureWhenCallSucceeds(t *testing.T) {
	closeErr := errors.New("cursor is busy")

	var err error
	ReportClose(closeErr, &err, "project page")

	if !errors.Is(err, closeErr) {
		t.Fatalf("err = %v, want a wrapper around %v", err, closeErr)
	}
	if got, want := err.Error(), "close project page: cursor is busy"; got != want {
		t.Fatalf("err.Error() = %q, want %q", got, want)
	}
}

func TestReportCloseKeepsTheOriginalFailure(t *testing.T) {
	scanErr := errors.New("scan record")

	err := scanErr
	ReportClose(errors.New("cursor is busy"), &err, "project page")

	if err != scanErr { //nolint:errorlint // The original error must survive unwrapped, not merely be reachable.
		t.Fatalf("err = %v, want the untouched original %v", err, scanErr)
	}
}

func TestReportCloseLeavesASucceedingCallAlone(t *testing.T) {
	var err error
	ReportClose(nil, &err, "project page")

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestReportCloseWithGivesTheCallerTheClosePhrase(t *testing.T) {
	sentinel := errors.New("sandbox listing cache failure")
	closeErr := errors.New("cursor is busy")

	var err error
	ReportCloseWith(closeErr, &err, "schema validation query", func(phrase string, cause error) error {
		return fmt.Errorf("%w: %s: %w", sentinel, phrase, cause)
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the caller's sentinel in the chain", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("err = %v, want the close failure in the chain", err)
	}
	if got, want := err.Error(), "sandbox listing cache failure: close schema validation query: cursor is busy"; got != want {
		t.Fatalf("err.Error() = %q, want %q", got, want)
	}
}
