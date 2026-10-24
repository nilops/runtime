package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// DigestArtifacts computes SHA-256 over the snapshot's on-disk artifacts,
// returning digests keyed by artifact name and per-artifact errors (NilOps
// snapshot-governance v0.2: hash-at-pause evidence chain). Failures are
// RETURNED, never swallowed: a missing digest must stay distinguishable from
// the feature being off. MemoryExportDeferred snapshots omit the memfile
// digest — its async seal has not finished — and report that as an error so
// the log shows why the key is absent.
func (s *Snapshot) DigestArtifacts(ctx context.Context) (map[string]string, map[string]error) {
	out := map[string]string{}
	errs := map[string]error{}

	digest := func(name, path string) {
		if path == "" {
			return
		}
		d, err := sha256File(path)
		if err != nil {
			errs[name] = err
			return
		}
		out[name] = d
	}

	if p, err := s.RootfsDiff.CachePath(ctx); err == nil {
		digest("rootfs_diff", p)
	} else {
		errs["rootfs_diff"] = err
	}
	if s.MemoryExportDeferred {
		errs["memfile_diff"] = fmt.Errorf("skipped: memory export deferred (async seal); digest after WaitMemorySealed")
	} else if p, err := s.MemorySnapshot.Diff.CachePath(ctx); err == nil {
		digest("memfile_diff", p)
	} else {
		errs["memfile_diff"] = err
	}
	if s.Snapfile != nil {
		digest("snapfile", s.Snapfile.Path())
	}
	if s.Metafile != nil {
		digest("metafile", s.Metafile.Path())
	}

	return out, errs
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
