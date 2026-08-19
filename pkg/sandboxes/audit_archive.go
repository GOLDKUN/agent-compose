package sandboxes

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	domain "agent-compose/pkg/model"
)

const (
	sandboxArchiveWorkspaceDirectory       = "workspace"
	sandboxArchiveExternalVolumesDirectory = "volumes"
)

type sandboxArchiveManifest struct {
	Version    int       `json:"version"`
	ArchiveID  string    `json:"archive_id"`
	SandboxID  string    `json:"sandbox_id"`
	ArchivedAt time.Time `json:"archived_at"`
	StoppedAt  time.Time `json:"stopped_at"`
	SizeBytes  int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	Includes   []string  `json:"includes"`
	Excludes   []string  `json:"excludes"`
}

func (c *SandboxRetentionCleaner) archiveSandbox(ctx context.Context, sandbox *domain.Sandbox) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sandbox.Archive == nil {
		now := c.now()
		sandbox.Archive = &domain.SandboxArchive{
			State:     domain.SandboxArchiveStateArchiving,
			ID:        now.Format("20060102T150405.000000000Z"),
			StartedAt: now,
		}
		if err := c.Store.UpdateSandbox(ctx, sandbox); err != nil {
			return fmt.Errorf("persist sandbox archive intent: %w", err)
		}
	}
	if sandbox.Archive.State != domain.SandboxArchiveStateArchiving && sandbox.Archive.State != domain.SandboxArchiveStateArchived {
		return fmt.Errorf("unknown sandbox archive state %q", sandbox.Archive.State)
	}
	if sandbox.Archive.State == domain.SandboxArchiveStateArchiving {
		size, checksum, err := c.writeSandboxArchive(ctx, sandbox)
		if err != nil {
			sandbox.Archive.LastError = err.Error()
			_ = c.Store.UpdateSandbox(ctx, sandbox)
			return err
		}
		sandbox.Archive.State = domain.SandboxArchiveStateArchived
		sandbox.Archive.CompletedAt = c.now()
		sandbox.Archive.SizeBytes = size
		sandbox.Archive.SHA256 = checksum
		sandbox.Archive.LastError = ""
		if err := c.Store.UpdateSandbox(ctx, sandbox); err != nil {
			return fmt.Errorf("persist completed sandbox archive: %w", err)
		}
	}
	return nil
}

// openSandboxArchiveDirectory ensures the sandbox's archive directory exists,
// re-validating after creation so a previously missing path cannot resolve
// through a symlink into the sandbox tree, and returns it opened alongside a
// directory handle usable for fsync. The caller owns closing directory and
// directoryHandle.
func (c *SandboxRetentionCleaner) openSandboxArchiveDirectory(sandbox *domain.Sandbox) (archiveDir string, directory *os.Root, directoryHandle *os.File, err error) {
	archiveDir, err = c.safeArchiveDir(sandbox.Summary.ID)
	if err != nil {
		return "", nil, nil, err
	}
	archiveRoot := filepath.Dir(archiveDir)
	if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
		return "", nil, nil, fmt.Errorf("create sandbox archive root: %w", err)
	}
	if _, err := c.safeArchiveDir(sandbox.Summary.ID); err != nil {
		return "", nil, nil, err
	}
	root, err := os.OpenRoot(archiveRoot)
	if err != nil {
		return "", nil, nil, fmt.Errorf("open sandbox archive root: %w", err)
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir(sandbox.Summary.ID, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, nil, fmt.Errorf("create sandbox archive directory: %w", err)
	}
	info, err := root.Lstat(sandbox.Summary.ID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("inspect sandbox archive directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, nil, fmt.Errorf("sandbox archive directory %q is not a safe directory", archiveDir)
	}
	directory, err = root.OpenRoot(sandbox.Summary.ID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("open sandbox archive directory: %w", err)
	}
	directoryHandle, err = directory.Open(".")
	if err != nil {
		_ = directory.Close()
		return "", nil, nil, fmt.Errorf("open sandbox archive directory handle: %w", err)
	}
	return archiveDir, directory, directoryHandle, nil
}

func (c *SandboxRetentionCleaner) writeSandboxArchive(ctx context.Context, sandbox *domain.Sandbox) (int64, string, error) {
	archiveDir, directory, directoryHandle, err := c.openSandboxArchiveDirectory(sandbox)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = directory.Close() }()
	defer func() { _ = directoryHandle.Close() }()
	if err := syncDirectoryChain(archiveDir); err != nil {
		return 0, "", fmt.Errorf("persist sandbox archive directory: %w", err)
	}

	if err := validateArchiveID(sandbox.Archive.ID); err != nil {
		return 0, "", err
	}
	archiveName := sandbox.Archive.ID + ".tar.zst"
	temporaryName := archiveName + ".tmp"
	if err := removeArchiveTemporary(directory, temporaryName); err != nil {
		return 0, "", err
	}
	manifestName := sandbox.Archive.ID + ".json"
	if manifest, committed, err := recoverCommittedSandboxArchive(
		ctx, directory, directoryHandle, sandboxArchiveIdentity{SandboxID: sandbox.Summary.ID, ArchiveID: sandbox.Archive.ID},
	); err != nil {
		return 0, "", err
	} else if committed {
		return manifest.SizeBytes, manifest.SHA256, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	if err := discardInvalidCommittedArchive(directory, archiveName, manifestName); err != nil {
		return 0, "", err
	}
	if err := syncOpenDirectory(directoryHandle); err != nil {
		return 0, "", fmt.Errorf("persist discarded sandbox archive: %w", err)
	}
	file, err := directory.OpenFile(temporaryName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("create sandbox archive: %w", err)
	}
	hash := sha256.New()
	counted := &countingWriter{writer: io.MultiWriter(file, hash)}
	zstdWriter, err := zstd.NewWriter(counted)
	if err != nil {
		_ = file.Close()
		_ = directory.Remove(temporaryName)
		return 0, "", fmt.Errorf("create zstd archive writer: %w", err)
	}
	tarWriter := tar.NewWriter(zstdWriter)
	writeErr := c.writeSandboxArchiveEntries(ctx, tarWriter, sandbox)
	closeErr := errors.Join(tarWriter.Close(), zstdWriter.Close(), file.Sync(), file.Close())
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = directory.Remove(temporaryName)
		return 0, "", fmt.Errorf("write sandbox archive: %w", err)
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	if err := directory.Rename(temporaryName, archiveName); err != nil {
		_ = directory.Remove(temporaryName)
		return 0, "", fmt.Errorf("commit sandbox archive: %w", err)
	}
	manifest := sandboxArchiveManifest{
		Version: sandboxArchiveManifestVersion, ArchiveID: sandbox.Archive.ID, SandboxID: sandbox.Summary.ID,
		ArchivedAt: c.now(), SizeBytes: counted.total, SHA256: checksum,
		Includes: []string{"sandbox/**", ".lifecycle/ownership.json"},
		Excludes: []string{"sandbox/workspace/**", "sandbox/volumes/**", "driver-private runtime"},
	}
	if state, stateErr := c.Store.GetVMState(sandbox.Summary.ID); stateErr == nil {
		manifest.StoppedAt = state.StoppedAt.UTC()
	}
	if err := writeArchiveManifest(directory, manifestName, manifest); err != nil {
		return 0, "", err
	}
	if err := syncOpenDirectory(directoryHandle); err != nil {
		return 0, "", fmt.Errorf("persist committed sandbox archive: %w", err)
	}
	return counted.total, checksum, nil
}

func recoverCommittedSandboxArchive(
	ctx context.Context,
	directory *os.Root,
	directoryHandle *os.File,
	identity sandboxArchiveIdentity,
) (sandboxArchiveManifest, bool, error) {
	manifest, err := validateCommittedSandboxArchiveInDirectory(ctx, directory, identity.SandboxID, identity.ArchiveID)
	if err != nil {
		return sandboxArchiveManifest{}, false, nil
	}
	// A prior attempt may have renamed both committed files but failed its
	// final directory sync. Make durability explicit before metadata can move
	// to archived and authorize removal of the originals.
	if err := syncOpenDirectory(directoryHandle); err != nil {
		return sandboxArchiveManifest{}, true, fmt.Errorf("persist recovered sandbox archive: %w", err)
	}
	return manifest, true, nil
}

func (c *SandboxRetentionCleaner) writeSandboxArchiveEntries(ctx context.Context, writer *tar.Writer, sandbox *domain.Sandbox) error {
	sandboxDir := c.Store.SandboxDir(sandbox.Summary.ID)
	if err := filepath.WalkDir(sandboxDir, func(path string, item fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("archive entry %q has unsupported mode %s", path, info.Mode())
		}
		relative, err := sandboxArchiveRelativePath(sandboxDir, path)
		if err != nil {
			return fmt.Errorf("archive entry %q escapes sandbox", path)
		}
		if relative == sandboxArchiveWorkspaceDirectory || relative == sandboxArchiveExternalVolumesDirectory {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(path)
			if err != nil {
				return err
			}
			if err := validateArchiveSymlinkTarget(relative, linkTarget); err != nil {
				return fmt.Errorf("archive symlink %q: %w", path, err)
			}
		}
		header, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(filepath.Join("sandbox", relative))
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, contextReader{ctx: ctx, reader: file})
		return errors.Join(copyErr, file.Close())
	}); err != nil {
		return err
	}
	if strings.TrimSpace(c.SandboxRoot) != "" {
		ownershipPath, err := OwnershipRecordPath(c.SandboxRoot, sandbox.Summary.ID)
		if err != nil {
			return err
		}
		if err := writeArchiveFile(ctx, writer, ownershipPath, ".lifecycle/ownership.json"); err != nil {
			return fmt.Errorf("archive sandbox ownership record: %w", err)
		}
	}
	return nil
}

func sandboxArchiveRelativePath(sandboxDir, path string) (string, error) {
	relative, err := filepath.Rel(sandboxDir, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox")
	}
	return relative, nil
}

func writeArchiveFile(ctx context.Context, writer *tar.Writer, path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("archive entry %q is not a regular file", path)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, contextReader{ctx: ctx, reader: file})
	return errors.Join(copyErr, file.Close())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func (c *SandboxRetentionCleaner) safeArchiveDir(sandboxID string) (string, error) {
	return safeSandboxArchiveDir(c.ArchiveRoot, c.SandboxRoot, sandboxID)
}

func writeArchiveManifest(directory *os.Root, name string, manifest sandboxArchiveManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sandbox archive manifest: %w", err)
	}
	temporaryName := name + ".tmp"
	if err := removeArchiveTemporary(directory, temporaryName); err != nil {
		return err
	}
	file, err := directory.OpenFile(temporaryName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create sandbox archive manifest: %w", err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := errors.Join(file.Sync(), file.Close())
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = directory.Remove(temporaryName)
		return fmt.Errorf("write sandbox archive manifest: %w", err)
	}
	if err := directory.Rename(temporaryName, name); err != nil {
		_ = directory.Remove(temporaryName)
		return fmt.Errorf("commit sandbox archive manifest: %w", err)
	}
	return nil
}

func removeArchiveTemporary(directory *os.Root, name string) error {
	if err := directory.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale sandbox archive temporary file: %w", err)
	}
	return nil
}

func discardInvalidCommittedArchive(directory *os.Root, names ...string) error {
	for _, name := range names {
		if err := directory.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("discard invalid committed sandbox archive file %q: %w", name, err)
		}
		if err := removeArchiveTemporary(directory, name+".tmp"); err != nil {
			return err
		}
	}
	return nil
}

func validateArchiveSymlinkTarget(relative, target string) error {
	if filepath.IsAbs(target) || filepath.VolumeName(target) != "" {
		return fmt.Errorf("target %q is absolute", target)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(relative), target))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %q escapes sandbox", target)
	}
	return nil
}

func syncDirectoryChain(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if err := syncDirectory(current); err != nil {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func syncOpenDirectory(directory *os.File) error {
	if err := directory.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

type countingWriter struct {
	writer io.Writer
	total  int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	w.total += int64(written)
	return written, err
}
