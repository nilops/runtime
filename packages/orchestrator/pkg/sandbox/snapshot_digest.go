package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// DigestArtifacts computes SHA-256 over the snapshot's on-disk artifacts and
// returns them keyed by artifact name (nilOps snapshot-governance v0.2:
// hash-at-pause evidence chain). MemoryExportDeferred snapshots omit the
// memfile digest rather than hash a file the async seal has not finished
// writing; the caller can digest it again once WaitMemorySealed returns.
func (s *Snapshot) DigestArtifacts(ctx context.Context) map[string]string {
	out := map[string]string{}

	if p, err := s.RootfsDiff.CachePath(ctx); err == nil && p != "" {
		if d, err := sha256File(p); err == nil {
			out["rootfs_diff"] = d
		}
	}
	if !s.MemoryExportDeferred {
		if p, err := s.MemorySnapshot.Diff.CachePath(ctx); err == nil && p != "" {
			if d, err := sha256File(p); err == nil {
				out["memfile_diff"] = d
			}
		}
	}
	if s.Snapfile != nil && s.Snapfile.Path() != "" {
		if d, err := sha256File(s.Snapfile.Path()); err == nil {
			out["snapfile"] = d
		}
	}
	if s.Metafile != nil && s.Metafile.Path() != "" {
		if d, err := sha256File(s.Metafile.Path()); err == nil {
			out["metafile"] = d
		}
	}

	return out
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // read-only digest of our own artifact
	if err != nil {
		return "", fmt.Errorf("digest open: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("digest read: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
