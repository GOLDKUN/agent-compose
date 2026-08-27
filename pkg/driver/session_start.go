package driver

import (
	appconfig "agent-compose/pkg/config"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const defaultImagePullTimeout = 10 * time.Minute

// SandboxStartTarget identifies the sandbox and runtime driver to prepare for start.
type SandboxStartTarget struct {
	Driver  string
	Session *Sandbox
	VMState VMState
}

func PrepareSandboxStart(ctx context.Context, config *appconfig.Config, target SandboxStartTarget) (VMState, error) {
	pullTimeout := config.ImagePullTimeout
	if pullTimeout <= 0 {
		pullTimeout = defaultImagePullTimeout
	}
	pullPolicy := strings.ToLower(strings.TrimSpace(target.Session.Summary.PullPolicy))
	return prepareSandboxStartWithResolver(ctx, config, sandboxStartResolveRequest{
		Driver:      target.Driver,
		Session:     target.Session,
		VMState:     target.VMState,
		PullPolicy:  pullPolicy,
		PullTimeout: pullTimeout,
	}, dockerFirstRuntimeImageResolver{ensureDocker: ensureDockerImage})
}

// sandboxStartResolveRequest is SandboxStartTarget plus the resolved pull policy and
// timeout needed to prepare the sandbox's guest image.
type sandboxStartResolveRequest struct {
	Driver      string
	Session     *Sandbox
	VMState     VMState
	PullPolicy  string
	PullTimeout time.Duration
}

func prepareSandboxStartWithResolver(ctx context.Context, config *appconfig.Config, request sandboxStartResolveRequest, resolver runtimeImageResolver) (VMState, error) {
	driver, session, vmState, pullPolicy, pullTimeout :=
		request.Driver, request.Session, request.VMState, request.PullPolicy, request.PullTimeout
	if _, err := prepareRuntimeMountManifest(config, session, driver); err != nil {
		return vmState, err
	}
	vmState.Image = resolveSandboxGuestImage(vmState.Image, session.Summary.GuestImage, defaultGuestImageForDriver(config, driver))
	switch driver {
	case RuntimeDriverBoxlite:
		if err := ensureRuntimeAssets(config.BoxRootfsPath); err != nil {
			return vmState, err
		}
		vmState.Registry = config.ImageRegistry
		if vmState.Image != "" {
			slog.Info("agent-compose resolving boxlite guest image", "sandbox_id", session.Summary.ID, "guest_image", vmState.Image)
			resolvedImage, err := resolver.ResolvePrepareImage(ctx, config, imageResolveRequest{
				Driver: driver, ImageRef: vmState.Image, PullPolicy: pullPolicy, PullTimeout: pullTimeout,
			})
			if err != nil {
				return vmState, err
			}
			vmState.Image = resolveSandboxGuestImage(resolvedImage, vmState.Image)
		}
	case RuntimeDriverDocker:
		vmState.Registry = ""
		if vmState.Image != "" {
			slog.Info("agent-compose ensuring docker guest image", "sandbox_id", session.Summary.ID, "guest_image", vmState.Image)
			resolvedImage, err := resolver.ResolvePrepareImage(ctx, config, imageResolveRequest{
				Driver: driver, ImageRef: vmState.Image, PullPolicy: pullPolicy, PullTimeout: pullTimeout,
			})
			if err != nil {
				return vmState, err
			}
			vmState.Image = resolveSandboxGuestImage(resolvedImage, vmState.Image)
		}
	case RuntimeDriverMicrosandbox:
		vmState.Registry = ""
	case RuntimeDriverK8s:
		vmState.Registry = ""
	default:
		return vmState, fmt.Errorf("unsupported agent-compose runtime driver %q", driver)
	}
	return vmState, nil
}

// imageResolveRequest describes the guest image to resolve for one sandbox start.
type imageResolveRequest struct {
	Driver      string
	ImageRef    string
	PullPolicy  string
	PullTimeout time.Duration
}

type runtimeImageResolver interface {
	ResolvePrepareImage(context.Context, *appconfig.Config, imageResolveRequest) (string, error)
}

type dockerFirstRuntimeImageResolver struct {
	ensureDocker func(context.Context, string, string, time.Duration) (string, error)
}

func (r dockerFirstRuntimeImageResolver) ResolvePrepareImage(ctx context.Context, config *appconfig.Config, request imageResolveRequest) (string, error) {
	imageRef := strings.TrimSpace(request.ImageRef)
	if imageRef == "" {
		return "", nil
	}
	switch request.Driver {
	case RuntimeDriverDocker:
		ensure := r.ensureDocker
		if ensure == nil {
			ensure = ensureDockerImage
		}
		return ensure(ctx, imageRef, request.PullPolicy, request.PullTimeout)
	case RuntimeDriverBoxlite, RuntimeDriverMicrosandbox, RuntimeDriverK8s:
		return imageRef, nil
	default:
		return "", fmt.Errorf("unsupported agent-compose runtime driver %q", request.Driver)
	}
}

func ensureRuntimeAssets(rootfs string) error {
	if strings.TrimSpace(rootfs) == "" {
		return nil
	}
	info, err := os.Stat(rootfs)
	if err != nil {
		return fmt.Errorf("agent-compose rootfs path missing %s: verify BOX_ROOTFS_PATH or switch to DEFAULT_IMAGE: %w", rootfs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("agent-compose rootfs path is not a directory: %s", rootfs)
	}
	return nil
}
