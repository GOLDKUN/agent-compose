package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveReleaseReportsManifestImages(t *testing.T) {
	options := DefaultOptions()
	options.BundleDir = makeTestBundle(t, "v2")
	options.InstallDir = filepath.Join(t.TempDir(), "install")
	service := Service{}

	release, err := service.ResolveRelease(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()

	want := ImageReferences{
		Backend:  "registry.example/agent-compose:v2",
		Frontend: "registry.example/agent-compose-ui:latest",
		Guest:    "registry.example/agent-compose-guest:v2",
	}
	if release.Images != want {
		t.Fatalf("release images = %#v, want %#v", release.Images, want)
	}
	preview, err := service.PreviewImages(OperationInstall, options, release)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Backend.Value != want.Backend || preview.Backend.Source != ImageSourceRelease ||
		preview.Frontend.Value != want.Frontend || preview.Guest.Value != want.Guest {
		t.Fatalf("install preview = %#v", preview)
	}
}

func TestPreviewImagesMatchesUpgradePreservation(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "install")
	writeTestFile(t, filepath.Join(installDir, ".env"), "AGENT_COMPOSE_IMAGE=operator.example/backend:keep\nAGENT_COMPOSE_FRONTEND_IMAGE=registry.example/agent-compose-ui:v1\nDEFAULT_IMAGE=registry.example/agent-compose-guest:v1\n", 0o600)
	writeTestFile(t, filepath.Join(installDir, ".installer-state.env"), "AGENT_COMPOSE_IMAGE=registry.example/agent-compose:v1\nAGENT_COMPOSE_FRONTEND_IMAGE=registry.example/agent-compose-ui:v1\nDEFAULT_IMAGE=registry.example/agent-compose-guest:v1\n", 0o600)
	options := DefaultOptions()
	options.BundleDir = makeTestBundle(t, "v2")
	options.InstallDir = installDir
	service := Service{}
	release, err := service.ResolveRelease(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()

	preview, err := service.PreviewImages(OperationUpgrade, options, release)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Backend != (ImageSelection{Value: "operator.example/backend:keep", Source: ImageSourcePreserved}) {
		t.Fatalf("backend preview = %#v", preview.Backend)
	}
	if preview.Frontend != (ImageSelection{Value: "registry.example/agent-compose-ui:latest", Source: ImageSourceRelease}) {
		t.Fatalf("frontend preview = %#v", preview.Frontend)
	}
	if preview.Guest != (ImageSelection{Value: "registry.example/agent-compose-guest:v2", Source: ImageSourceRelease}) {
		t.Fatalf("guest preview = %#v", preview.Guest)
	}
}

func TestApplyReleaseReusesResolvedBundle(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions()
	options.BundleDir = makeTestBundle(t, "v1")
	options.InstallDir = filepath.Join(root, "install")
	options.KVMPath = filepath.Join(root, "missing-kvm")
	options.NoStart = true
	service := Service{Runner: &fakeRunner{}}
	release, err := service.ResolveRelease(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()

	options.BundleDir = filepath.Join(root, "bundle-was-removed")
	result, err := service.ApplyRelease(context.Background(), OperationInstall, options, release)
	if err != nil {
		t.Fatal(err)
	}
	if result.GuestImage != "registry.example/agent-compose-guest:v1" {
		t.Fatalf("guest image = %q", result.GuestImage)
	}
	if _, err := os.Stat(filepath.Join(options.InstallDir, ".env")); err != nil {
		t.Fatal(err)
	}
}
