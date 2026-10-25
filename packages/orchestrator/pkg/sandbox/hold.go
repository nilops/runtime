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
// tears it down. Overridable with SANDBOX_RAM_HOLD_TTL (Go duration); a value
// that fails to parse as a positive duration is reported and ignored.
func RamHoldTTL() time.Duration {
	if v := os.Getenv("SANDBOX_RAM_HOLD_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "nilops: invalid SANDBOX_RAM_HOLD_TTL %q, using default %s\n", v, ramHoldTTLDefault)
	}
	return ramHoldTTLDefault
}

// holdState tracks the RAM-hold of one sandbox. Every transition of `held`
// happens under mu, so resume, TTL release and re-hold cannot interleave:
// ResumeFromHold and ClaimHoldForRelease are compare-and-set on `held`, and
// the TTL timer from a previous hold is stopped before a new one is armed.
type holdState struct {
	mu        sync.Mutex
	held      bool
	expiresAt time.Time
	timer     *time.Timer
	// listHidden keeps a sandbox out of the server's List response. It is set
	// for the whole life of a RAM-hold sandbox once held: the API pause chain
	// forgets the sandbox, so a visible one reads as an orphan and the
	// API-side reconciler kills it — during the hold AND after a resume, until
	// the control-plane re-registers it (NIL-209 owns that hook).
	listHidden bool
}

// Held reports whether the sandbox is currently held in RAM.
func (s *Sandbox) Held() bool {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.held
}

// HoldListHidden reports whether the sandbox must stay out of the server's
// List response: held, or resumed from a hold whose API record is gone.
func (s *Sandbox) HoldListHidden() bool {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.listHidden
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
// stop for the duration of the hold (a paused VM would fail them) and are
// re-created on ResumeFromHold — Checks cannot be restarted in place, their
// `stopped` flag is permanent, so a NEW instance is started, the same way the
// in-place resume path does it.
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
		// state; try to un-pause so the sandbox stays usable, and report BOTH
		// failures — a swallowed one would strand a paused VM nothing watches.
		if rerr := s.process.ResumeInPlace(ctx); rerr != nil {
			s.Checks = NewChecks(s)
			go s.Checks.Start(context.WithoutCancel(ctx))
			return fmt.Errorf("failed to pause VM for RAM-hold: %w (compensating resume also failed, VM may be stuck paused: %v)", err, rerr)
		}
		s.Checks = NewChecks(s)
		go s.Checks.Start(context.WithoutCancel(ctx))
		return fmt.Errorf("failed to pause VM for RAM-hold: %w", err)
	}

	s.hold.mu.Lock()
	s.hold.held = true
	s.hold.listHidden = true
	s.hold.expiresAt = time.Now().Add(RamHoldTTL())
	s.hold.mu.Unlock()
	return nil
}

// SetHoldTimer records the TTL timer armed by the server for the current
// hold, so ResumeFromHold and the next hold can stop it. A timer left running
// from a previous hold would tear the next hold down at the FIRST deadline.
func (s *Sandbox) SetHoldTimer(t *time.Timer) {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	s.hold.timer = t
}

// stopHoldTimerLocked stops and drains the current TTL timer, if any.
func (s *Sandbox) stopHoldTimerLocked() {
	if s.hold.timer != nil {
		s.hold.timer.Stop()
		s.hold.timer = nil
	}
}

// ResumeFromHold un-pauses the held sandbox's own Firecracker process. The
// memory never left the process, so there is no snapshot to load — this is
// deliberately NOT the snapshot-resume path. The sandbox stays hidden from
// List afterwards: the API forgot it at pause time and would kill it as an
// orphan within a poll cycle, destroying the memory the hold preserved.
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

	// Flip the state only after the VM is live again; the TTL release claims
	// the same flag, so at most one of the two wins.
	s.hold.mu.Lock()
	if !s.hold.held {
		s.hold.mu.Unlock()
		return fmt.Errorf("hold was released while resuming")
	}
	s.stopHoldTimerLocked()
	s.hold.held = false
	s.hold.expiresAt = time.Time{}
	s.hold.mu.Unlock()

	s.Checks = NewChecks(s)
	go s.Checks.Start(context.WithoutCancel(ctx))
	return nil
}

// ClaimHoldForRelease atomically ends the hold for a TTL teardown: it returns
// true exactly once per hold, so a racing ResumeFromHold and a stale timer
// from an earlier hold cannot both act, and the winner is decided under the
// same mutex both paths use.
func (s *Sandbox) ClaimHoldForRelease() bool {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	if !s.hold.held {
		return false
	}
	s.stopHoldTimerLocked()
	s.hold.held = false
	s.hold.expiresAt = time.Time{}
	return true
}

// forensicHoldTTLDefault bounds a forensic hold when SANDBOX_FORENSIC_HOLD_TTL
// is unset: evidence retention outlives a triage shift, unlike a RAM-hold.
const forensicHoldTTLDefault = 24 * time.Hour

// ForensicHoldTTL is how long a forensically held sandbox may stay before the
// orchestrator tears it down. Overridable with SANDBOX_FORENSIC_HOLD_TTL.
func ForensicHoldTTL() time.Duration {
	if v := os.Getenv("SANDBOX_FORENSIC_HOLD_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "nilops: invalid SANDBOX_FORENSIC_HOLD_TTL %q, using default %s\n", v, forensicHoldTTLDefault)
	}
	return forensicHoldTTLDefault
}

// MarkHeldAfterPause marks a sandbox held when the normal pause flow has
// already paused its VM and written the evidence snapshot (forensic hold,
// NIL-208 inc-3). It does not touch the VM or the checks — the pause flow
// owns both — it only arms the hold bookkeeping: the TTL release that
// replaces the immediate teardown, and the List hiding for whatever remains
// of the sandbox's life. The snapshot artifacts and their digests are the
// primary evidence; the paused VM is retained live as secondary evidence
// until the TTL (or a Delete) releases it.
func (s *Sandbox) MarkHeldAfterPause() {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	s.hold.held = true
	s.hold.listHidden = true
	s.hold.expiresAt = time.Now().Add(ForensicHoldTTL())
}
