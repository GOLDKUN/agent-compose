//go:build k8scompose

package driver

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadGuestFile and ReadGuestDir let the daemon pull data a guest process
// wrote back out of a sandbox Pod without any shared filesystem (see
// docs/design/k8s_pod_runtime_driver_design.md §2.1: the k8s driver mounts
// nothing). Both are plain SandboxRuntime.Exec calls under the hood - `cat`
// for one file, `tar` for a directory (the same primitive `kubectl cp` is
// built on) - so they only need whatever's already in the guest image (a
// POSIX shell, cat, tar), no image changes and no extra plumbing beyond
// Exec, which this driver already implements.
//
// Neither method is part of the SandboxRuntime interface: docker/boxlite/
// microsandbox have a real shared mount and have no need for them, so
// callers reach these through a type assertion (see
// pkg/agentcompose/adapters.GuestFileReader/GuestDirReader), the same
// pattern already used for Stats/IsSandboxAlive.

func (r *k8sRuntime) ReadGuestFile(ctx context.Context, sandbox *Sandbox, vmState VMState, guestPath string) ([]byte, error) {
	guestPath = strings.TrimSpace(guestPath)
	if guestPath == "" {
		return nil, fmt.Errorf("read guest file: guest path is required")
	}
	stdout, stderr, exitCode, err := r.execRaw(ctx, k8sExecRequest{
		Sandbox: sandbox,
		VMState: vmState,
		Spec:    ExecSpec{Command: "cat", Args: []string{guestPath}},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("read guest file %s: %w", guestPath, err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("read guest file %s: exit code %d: %s", guestPath, exitCode, strings.TrimSpace(string(stderr)))
	}
	return stdout, nil
}

func (r *k8sRuntime) ReadGuestDir(ctx context.Context, sandbox *Sandbox, vmState VMState, guestDir, hostDestDir string) error {
	guestDir = strings.TrimSpace(guestDir)
	if guestDir == "" {
		return fmt.Errorf("read guest dir: guest path is required")
	}
	if strings.TrimSpace(hostDestDir) == "" {
		return fmt.Errorf("read guest dir %s: host destination is required", guestDir)
	}
	stdout, stderr, exitCode, err := r.execRaw(ctx, k8sExecRequest{
		Sandbox: sandbox,
		VMState: vmState,
		Spec:    ExecSpec{Command: "tar", Args: []string{"cf", "-", "-C", guestDir, "."}},
	}, nil)
	if err != nil {
		return fmt.Errorf("read guest dir %s: %w", guestDir, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("read guest dir %s: exit code %d: %s", guestDir, exitCode, strings.TrimSpace(string(stderr)))
	}
	if err := os.MkdirAll(hostDestDir, 0o755); err != nil {
		return fmt.Errorf("create host destination %s: %w", hostDestDir, err)
	}
	if err := k8sExtractTarArchive(bytes.NewReader(stdout), hostDestDir); err != nil {
		return fmt.Errorf("extract guest dir %s into %s: %w", guestDir, hostDestDir, err)
	}
	return nil
}

// WriteGuestFile pushes content into a sandbox Pod at a guest-absolute
// path, for the daemon-writes/guest-reads direction (prompts, skills,
// generated MCP/provider config - see
// docs/design/k8s_pod_runtime_driver_design.md §2.1 and §6) that docker/
// boxlite get for free from their shared mount and the k8s driver doesn't.
//
// Content is streamed on stdin through the k8s exec subresource. This remains
// an internal k8s transfer primitive rather than adding a field used by only
// one backend to the driver-wide ExecSpec.
//
// Calls EnsureSandbox itself rather than assuming the Pod already exists:
// pkg/runs/sandbox_preparation.go calls PrepareSandboxAgentEnvironment
// (which is what ends up calling this, for prompts/skills/MCP config)
// *before* startProjectRunSandboxRuntime's driver.StartSandboxVM - the
// step that actually creates the Pod for every other driver too. That
// ordering is fine for docker/boxlite (their writes just land on the host
// filesystem, which the container mounts whenever it starts), but this
// push needs a running Pod to Exec into right now, not later - confirmed
// by a live E2E run against k3d, where the first push of a run reliably
// hit "pod is not running" without this. EnsureSandbox's own find-or-create
// logic makes this safe to call defensively: idempotent, so it's a no-op
// once the real StartSandboxVM call reaches this same sandbox later.
func (r *k8sRuntime) WriteGuestFile(ctx context.Context, sandbox *Sandbox, vmState VMState, guestPath string, content []byte) error {
	guestPath = strings.TrimSpace(guestPath)
	if guestPath == "" {
		return fmt.Errorf("write guest file: guest path is required")
	}
	if !filepath.IsAbs(guestPath) {
		return fmt.Errorf("write guest file: guest path %s must be absolute", guestPath)
	}
	if _, err := r.EnsureSandbox(ctx, sandbox, vmState, ProxyState{}); err != nil {
		return fmt.Errorf("write guest file %s: ensure sandbox: %w", guestPath, err)
	}
	script := fmt.Sprintf(
		"mkdir -p %s && cat > %s",
		shellQuote(filepath.Dir(guestPath)),
		shellQuote(guestPath),
	)
	result, err := r.execWithInput(ctx, k8sExecRequest{
		Sandbox: sandbox,
		VMState: vmState,
		Spec:    ExecSpec{Command: "sh", Args: []string{"-c", script}},
	}, bytes.NewReader(content), nil)
	if err != nil {
		return fmt.Errorf("write guest file %s: %w", guestPath, err)
	}
	if !result.Success {
		return fmt.Errorf("write guest file %s: exit code %d: %s", guestPath, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// WriteGuestDir pushes a local host directory tree into a sandbox Pod at a
// guest-absolute path (for example a resolved workspace or skill directory).
// The tar archive is produced and consumed as a stream so its size does not
// become daemon heap usage.
func (r *k8sRuntime) WriteGuestDir(ctx context.Context, sandbox *Sandbox, vmState VMState, hostSrcDir, guestDir string) error {
	hostSrcDir = strings.TrimSpace(hostSrcDir)
	guestDir = strings.TrimSpace(guestDir)
	if hostSrcDir == "" {
		return fmt.Errorf("write guest dir: host source directory is required")
	}
	if guestDir == "" {
		return fmt.Errorf("write guest dir: guest path is required")
	}
	if !filepath.IsAbs(guestDir) {
		return fmt.Errorf("write guest dir: guest path %s must be absolute", guestDir)
	}
	if filepath.Clean(guestDir) == string(filepath.Separator) {
		return fmt.Errorf("write guest dir: refusing to replace guest root")
	}
	if k8sGuestDirOverlapsVolumeMount(sandbox, guestDir) {
		// guestDir is (or contains) a PVC-backed named volume mount. The
		// script below does `rm -rf` on guestDir before restoring it from
		// the daemon's host-side snapshot; running that against a mounted
		// volume would destroy whatever the guest itself already persisted
		// there across Pod recreations - exactly the durable state a PVC
		// mount is for. Skip the push and leave the volume's own content as
		// the source of truth.
		return nil
	}
	info, err := os.Stat(hostSrcDir)
	if err != nil {
		return fmt.Errorf("write guest dir %s: inspect host source %s: %w", guestDir, hostSrcDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("write guest dir %s: host source %s is not a directory", guestDir, hostSrcDir)
	}
	if _, err := r.EnsureSandbox(ctx, sandbox, vmState, ProxyState{}); err != nil {
		return fmt.Errorf("write guest dir %s: ensure sandbox: %w", guestDir, err)
	}
	archiveReader, archiveWriter := io.Pipe()
	archiveErr := make(chan error, 1)
	go func() {
		archiveErr <- archiveGuestDir(ctx, archiveWriter, hostSrcDir)
		close(archiveErr)
	}()
	script := fmt.Sprintf(
		"rm -rf %s && mkdir -p %s && tar xf - -C %s",
		shellQuote(guestDir),
		shellQuote(guestDir),
		shellQuote(guestDir),
	)
	result, execErr := r.execWithInput(ctx, k8sExecRequest{
		Sandbox: sandbox,
		VMState: vmState,
		Spec:    ExecSpec{Command: "sh", Args: []string{"-c", script}},
	}, archiveReader, nil)
	_ = archiveReader.CloseWithError(execErr)
	packErr := <-archiveErr
	if packErr != nil && (!errors.Is(packErr, io.ErrClosedPipe) || (execErr == nil && result.Success)) {
		return fmt.Errorf("archive %s: %w", hostSrcDir, packErr)
	}
	if execErr != nil {
		return fmt.Errorf("write guest dir %s: %w", guestDir, execErr)
	}
	if !result.Success {
		return fmt.Errorf("write guest dir %s: exit code %d: %s", guestDir, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// k8sGuestDirOverlapsVolumeMount reports whether guestDir is exactly the
// target of one of the sandbox's k8s named-volume (PVC) mounts. A
// destructive `rm -rf` push (WriteGuestDir) into such a path would delete
// the mounted volume's actual persistent content, not just a stale copy.
//
// This only needs to check for an exact match: podVolumeSpecs
// (k8sValidateVolumeMountTarget) already rejects a mount target that
// partially overlaps GuestWorkspacePath/GuestHomePath - the paths
// WriteGuestDir ever pushes to - at Pod-creation time, so a partial overlap
// can never reach this far. That rejection exists precisely because there
// is no correct way to handle a partial overlap here: excluding a mounted
// sub-path from the rm -rf/tar-restore would leave the rest of guestDir
// synced, but silently skipping the whole push (as an earlier version of
// this function did for any overlap, not just an exact one) means the
// daemon's workspace/home snapshot stops reaching the guest at all - a
// second, quieter form of the same "data goes stale in a way nothing
// surfaces" problem this function exists to prevent.
func k8sGuestDirOverlapsVolumeMount(sandbox *Sandbox, guestDir string) bool {
	if sandbox == nil {
		return false
	}
	guestDir = filepath.Clean(guestDir)
	for _, mount := range sandbox.VolumeMounts {
		if strings.ToLower(strings.TrimSpace(mount.Type)) != "volume" || strings.TrimSpace(mount.Driver) != RuntimeDriverK8s {
			continue
		}
		target := strings.TrimSpace(mount.Target)
		if target == "" {
			continue
		}
		if filepath.Clean(target) == guestDir {
			return true
		}
	}
	return false
}

// k8sPathIsWithin reports whether the cleaned absolute path is equal to, or
// a descendant of, root. Plain string concatenation (root+separator) mishandles
// root == "/", where the join would otherwise require a "//" prefix.
func k8sPathIsWithin(path, root string) bool {
	if path == root || root == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// buildTarArchive packs hostSrcDir's contents (relative paths, no leading
// directory entry for hostSrcDir itself) into a tar byte stream for
// WriteGuestDir.
func buildTarArchive(hostSrcDir string) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeTarArchive(&buf, hostSrcDir); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func archiveGuestDir(ctx context.Context, destination *io.PipeWriter, hostSrcDir string) error {
	err := writeTarArchive(destination, hostSrcDir)
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return errors.Join(err, destination.CloseWithError(err))
}

func writeTarArchive(destination io.Writer, hostSrcDir string) error {
	writer := tar.NewWriter(destination)
	walkErr := filepath.WalkDir(hostSrcDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, relErr := filepath.Rel(hostSrcDir, path)
		if relErr != nil {
			return relErr
		}
		if relPath == "." {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, infoErr = os.Readlink(path)
			if infoErr != nil {
				return infoErr
			}
		}
		header, headerErr := tar.FileInfoHeader(info, linkTarget)
		if headerErr != nil {
			return headerErr
		}
		header.Name = filepath.ToSlash(relPath)
		if entry.IsDir() {
			header.Name += "/"
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive path %s has unsupported file type %s", path, info.Mode().Type())
		}
		file, openErr := os.Open(path) //nolint:gosec // hostSrcDir is a resolved skill/config directory the daemon itself manages, not attacker-controlled input
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr != nil {
		_ = writer.Close()
		return walkErr
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return nil
}

// k8sExtractTarArchive extracts a tar stream into destDir, rejecting any entry
// whose path would resolve outside destDir (a defensively-applied guard
// against a malicious/corrupt archive, same class of check `tar`/`kubectl
// cp` itself applies - not expected to trigger against a well-formed
// archive from our own ReadGuestDir tar call).
func k8sExtractTarArchive(r io.Reader, destDir string) error {
	reader := tar.NewReader(r)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		target := filepath.Join(destDir, filepath.Clean(filepath.FromSlash(header.Name)))
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != filepath.Clean(destDir) {
			return fmt.Errorf("tar entry %q escapes destination directory", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create directory %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create parent directory for %s: %w", target, err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode&0o777)) //nolint:gosec // tar mode bits, masked to drop setuid/setgid/sticky
			if err != nil {
				return fmt.Errorf("create file %s: %w", target, err)
			}
			_, copyErr := io.Copy(file, reader) //nolint:gosec // bounded by the guest's own tar output, not an untrusted external source
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("write file %s: %w", target, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close file %s: %w", target, closeErr)
			}
		default:
			// Symlinks, devices, etc. are not expected from our own
			// ReadGuestDir tar call; skip rather than fail the whole pull.
			continue
		}
	}
}
