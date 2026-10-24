//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// RAM-hold (NIL-208 increment 2, node-gated prototype). A held sandbox keeps
// its whole VM — memory, vsock channel, in-flight guest state — inside the
// still-running Firecracker process with the vCPUs paused, and writes nothing
// to disk: engagement memory is secrets-by-default. The hold ends by resume
// (the sandbox's own Create un-pauses the same process; see the RAM-hold
// branch in server.Create) or by the TTL teardown (server.releaseHeldSandbox).

// ramHoldTTLDefault bounds a hold when SANDBOX_RAM_HOLD_TTL is unset.
const ramHoldTTLDefault = 30 * time.Minute

// RamHoldTTL is how long a held sandbox may stay in RAM before the orchestrator
// tears it down. Overridable with SANDBOX_RAM_HOLD_TTL (Go duration).
func RamHoldTTL() time.Duration {
	if v := os.Getenv("SANDBOX_RAM_HOLD_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return ramHoldTTLDefault
}

// holdState tracks the RAM-hold of one sandbox.
type holdState struct {
	mu        sync.Mutex
	held      bool
	expiresAt time.Time
}

// Held reports whether the sandbox is currently held in RAM.
func (s *Sandbox) Held() bool {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.held
}

// HoldExpiresAt reports when the hold ends; zero when not held.
func (s *Sandbox) HoldExpiresAt() time.Time {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	if !s.hold.held {
		return time.Time{}
	}
	return s.hold.expiresAt
}

// HoldInPlace pauses the guest VM and marks the sandbox held. No snapshot is
// written; the Firecracker process keeps the memory resident. Health checks
// stop for the duration of the hold (a paused VM would fail them) and restart
// on ResumeFromHold.
func (s *Sandbox) HoldInPlace(ctx context.Context) error {
	s.hold.mu.Lock()
	already := s.hold.held
	s.hold.mu.Unlock()
	if already {
		return fmt.Errorf("sandbox is already held")
	}

	s.Checks.Stop()
	if err := s.process.Pause(ctx); err != nil {
		// The pause PATCH failing leaves the VM running or in an ambiguous
		// state; un-pause and restart the checks so the sandbox stays usable.
		_ = s.process.ResumeInPlace(ctx)
		s.Checks.Start(ctx)
		return fmt.Errorf("failed to pause VM for RAM-hold: %w", err)
	}

	s.hold.mu.Lock()
	s.hold.held = true
	s.hold.expiresAt = time.Now().Add(RamHoldTTL())
	s.hold.mu.Unlock()
	return nil
}

// ResumeFromHold un-pauses the held sandbox's own Firecracker process. The
// memory never left the process, so there is no snapshot to load — this is
// deliberately NOT the snapshot-resume path.
func (s *Sandbox) ResumeFromHold(ctx context.Context) error {
	s.hold.mu.Lock()
	held := s.hold.held
	s.hold.mu.Unlock()
	if !held {
		return fmt.Errorf("sandbox is not held")
	}

	if err := s.process.ResumeInPlace(ctx); err != nil {
		return fmt.Errorf("failed to resume VM from RAM-hold: %w", err)
	}

	s.hold.mu.Lock()
	s.hold.held = false
	s.hold.expiresAt = time.Time{}
	s.hold.mu.Unlock()

	s.Checks.Start(ctx)
	return nil
}
