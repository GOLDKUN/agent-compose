package sandboxes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const sandboxArchiveManifestVersion = 1

func validateCommittedSandboxArchive(
	ctx context.Context,
	archiveRoot string,
	sandboxRoot string,
	sandboxID string,
	archiveID string,
) (sandboxArchiveManifest, error) {
	archiveDir, err := safeSandboxArchiveDir(archiveRoot, sandboxRoot, sandboxID)
	if err != nil {
		return sandboxArchiveManifest{}, err
	}
	archiveParent := filepath.Dir(archiveDir)
	root, err := os.OpenRoot(archiveParent)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("open sandbox archive root: %w", err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(sandboxID)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("inspect sandbox archive directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return sandboxArchiveManifest{}, fmt.Errorf("sandbox archive directory %q is not a safe directory", archiveDir)
	}
	directory, err := root.OpenRoot(sandboxID)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("open sandbox archive directory: %w", err)
	}
	defer func() { _ = directory.Close() }()
	return validateCommittedSandboxArchiveInDirectory(ctx, directory, sandboxID, archiveID)
}

func validateCommittedSandboxArchiveInDirectory(
	ctx context.Context,
	directory *os.Root,
	sandboxID string,
	archiveID string,
) (sandboxArchiveManifest, error) {
	if err := validateArchiveID(archiveID); err != nil {
		return sandboxArchiveManifest{}, err
	}
	manifestName := archiveID + ".json"
	manifestInfo, err := directory.Lstat(manifestName)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("inspect sandbox archive manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() {
		return sandboxArchiveManifest{}, fmt.Errorf("sandbox archive manifest %q is not a regular file", manifestName)
	}
	if manifestInfo.Size() > 1<<20 {
		return sandboxArchiveManifest{}, fmt.Errorf("sandbox archive manifest %q is too large", manifestName)
	}
	manifestFile, err := directory.Open(manifestName)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("open sandbox archive manifest: %w", err)
	}
	encoded, readErr := io.ReadAll(io.LimitReader(manifestFile, 1<<20))
	closeErr := manifestFile.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("read sandbox archive manifest: %w", err)
	}
	var manifest sandboxArchiveManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("decode sandbox archive manifest: %w", err)
	}
	if manifest.Version != sandboxArchiveManifestVersion || manifest.SandboxID != sandboxID || manifest.ArchiveID != archiveID {
		return sandboxArchiveManifest{}, fmt.Errorf("sandbox archive manifest identity or version does not match")
	}

	archiveName := archiveID + ".tar.zst"
	archiveInfo, err := directory.Lstat(archiveName)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("inspect committed sandbox archive: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() {
		return sandboxArchiveManifest{}, fmt.Errorf("committed sandbox archive %q is not a regular file", archiveName)
	}
	if manifest.SizeBytes < 0 || archiveInfo.Size() != manifest.SizeBytes {
		return sandboxArchiveManifest{}, fmt.Errorf("committed sandbox archive size does not match manifest")
	}
	archiveFile, err := directory.Open(archiveName)
	if err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("open committed sandbox archive: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, contextReader{ctx: ctx, reader: archiveFile})
	closeErr = archiveFile.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return sandboxArchiveManifest{}, fmt.Errorf("checksum committed sandbox archive: %w", err)
	}
	if checksum := hex.EncodeToString(hash.Sum(nil)); checksum != manifest.SHA256 {
		return sandboxArchiveManifest{}, fmt.Errorf("committed sandbox archive checksum does not match manifest")
	}
	return manifest, nil
}

func safeSandboxArchiveDir(archiveRoot, sandboxRoot, sandboxID string) (string, error) {
	if sandboxID == "" || filepath.Base(sandboxID) != sandboxID || strings.ContainsAny(sandboxID, `/\\`) {
		return "", fmt.Errorf("invalid sandbox archive ID %q", sandboxID)
	}
	root, err := validateSandboxArchiveRoot(archiveRoot, sandboxRoot)
	if err != nil {
		return "", err
	}
	directory, err := filepath.Abs(filepath.Join(root, sandboxID))
	if err != nil || filepath.Dir(directory) != root {
		return "", fmt.Errorf("sandbox archive path escapes archive root")
	}
	return directory, nil
}

func validateSandboxArchiveRoot(archiveRoot, sandboxRoot string) (string, error) {
	archiveRoot = strings.TrimSpace(archiveRoot)
	if archiveRoot == "" {
		return "", fmt.Errorf("invalid sandbox archive root")
	}
	root, err := filepath.Abs(archiveRoot)
	if err != nil || root == "" {
		return "", fmt.Errorf("invalid sandbox archive root")
	}
	if strings.TrimSpace(sandboxRoot) != "" {
		resolvedSandboxRoot, resolveErr := resolvePathFromExistingAncestor(sandboxRoot)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve sandbox root: %w", resolveErr)
		}
		resolvedArchiveRoot, resolveErr := resolvePathFromExistingAncestor(root)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve sandbox archive root: %w", resolveErr)
		}
		relative, relativeErr := filepath.Rel(resolvedSandboxRoot, resolvedArchiveRoot)
		if relativeErr != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return "", fmt.Errorf("sandbox archive root %q must be outside sandbox root %q", root, sandboxRoot)
		}
	}
	return root, nil
}

func validateArchiveID(archiveID string) error {
	if archiveID == "" || archiveID != strings.TrimSpace(archiveID) || archiveID == "." || archiveID == ".." || filepath.Base(archiveID) != archiveID || strings.ContainsAny(archiveID, `/\\`) {
		return fmt.Errorf("invalid archive ID %q", archiveID)
	}
	return nil
}
