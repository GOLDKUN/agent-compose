//go:build linux && cgo && microsandboxcgo

package driver

import (
	"context"
	"errors"
	"strings"
	"testing"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

func TestListMicrosandboxManagedSandboxesCollectsEveryPage(t *testing.T) {
	firstCursor := "next"
	first := &microsandbox.SandboxHandle{}
	second := &microsandbox.SandboxHandle{}
	pages := []*microsandbox.SandboxPage{
		{Sandboxes: []*microsandbox.SandboxHandle{first}, NextCursor: &firstCursor},
		{Sandboxes: []*microsandbox.SandboxHandle{second}},
	}
	calls := 0

	handles, err := listMicrosandboxManagedSandboxes(context.Background(), func(context.Context, ...microsandbox.SandboxListOption) (*microsandbox.SandboxPage, error) {
		page := pages[calls]
		calls++
		return page, nil
	})
	if err != nil {
		t.Fatalf("listMicrosandboxManagedSandboxes() error = %v", err)
	}
	if calls != 2 || len(handles) != 2 || handles[0] != first || handles[1] != second {
		t.Fatalf("listMicrosandboxManagedSandboxes() calls = %d, handles = %#v", calls, handles)
	}
}

func TestListMicrosandboxManagedSandboxesRejectsInvalidPagination(t *testing.T) {
	tests := []struct {
		name     string
		listPage microsandboxSandboxPageLister
		want     string
	}{
		{
			name: "list error",
			listPage: func(context.Context, ...microsandbox.SandboxListOption) (*microsandbox.SandboxPage, error) {
				return nil, errors.New("list failed")
			},
			want: "list failed",
		},
		{
			name: "nil page",
			listPage: func(context.Context, ...microsandbox.SandboxListOption) (*microsandbox.SandboxPage, error) {
				return nil, nil
			},
			want: "empty page response",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := listMicrosandboxManagedSandboxes(context.Background(), test.listPage)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("listMicrosandboxManagedSandboxes() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestListMicrosandboxManagedSandboxesRejectsCursorCycle(t *testing.T) {
	cursors := []string{"cursor-a", "cursor-b", "cursor-a"}
	calls := 0

	_, err := listMicrosandboxManagedSandboxes(context.Background(), func(context.Context, ...microsandbox.SandboxListOption) (*microsandbox.SandboxPage, error) {
		page := &microsandbox.SandboxPage{NextCursor: &cursors[calls]}
		calls++
		return page, nil
	})
	if err == nil || !strings.Contains(err.Error(), "pagination cursor repeated") {
		t.Fatalf("listMicrosandboxManagedSandboxes() error = %v, want repeated cursor error", err)
	}
	if calls != len(cursors) {
		t.Fatalf("listMicrosandboxManagedSandboxes() calls = %d, want %d", calls, len(cursors))
	}
}
