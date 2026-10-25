//go:build linux

package finalize

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// vsockReadyTimeoutDefault bounds the readiness poll before the finalize
// snapshot. Replaces the timing the envd wait used to provide: a snapshot
// taken while the guest is still mid-boot resumes into sandboxes that never
// finish booting (NIL-207 diagnosis, 2026-10-24). Read per call — never a
// package-level mutable — so concurrent builds cannot race on it.
const vsockReadyTimeoutDefault = 30 * time.Second

// vsockReadyStrict, set by SANDBOX_VSOCK_READY_STRICT=true, makes a failed
// readiness poll FAIL the build instead of falling through with a warning.
// Fail-open keeps agent-less non-envd templates buildable; strict mode is
// for nodes whose templates are all supposed to carry an agent, where a
// wedged agent shipping as a green half-boot snapshot is exactly the
// NIL-207 failure mode this poll exists to refuse.
func vsockReadyStrict() bool {
	return os.Getenv("SANDBOX_VSOCK_READY_STRICT") == "true"
}

// waitForVsockAgent polls the build sandbox's NilOps guest agent over its
// Firecracker vsock UDS until it answers the health verb, then returns.
//
// The poll is the readiness signal for the snapshot: when the agent answers,
// the guest has reached multi-user with the agent listening, so the snapshot
// resumes into a fully booted sandbox. Nodes without SANDBOX_X_VSOCK_DIR, and
// guests that never answer (agent-less templates), fall through after the
// timeout and keep the old snapshot timing — callers decide whether that is
// fatal.
func waitForVsockAgent(ctx context.Context, sandboxID string) error {
	dir := os.Getenv("SANDBOX_VSOCK_DIR")
	if dir == "" {
		return nil
	}
	timeout := vsockReadyTimeoutDefault
	if v := os.Getenv("SANDBOX_SNAPSHOT_READY_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		} else {
			// Fail-loud on a misconfigured knob, not a silent default.
			fmt.Fprintf(os.Stderr, "nilops: invalid SANDBOX_SNAPSHOT_READY_TIMEOUT %q, using default %s\n", v, vsockReadyTimeoutDefault)
		}
	}
	udsPath := filepath.Join(dir, sandboxID+".vsock.sock")

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		err := vsockHealth(udsPath)
		if err == nil {
			return nil
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("vsock agent not ready within %s: %w", timeout, lastErr)
}

// vsockHealth runs one health call against the guest agent: dial the vsock
// UDS, complete Firecracker's "CONNECT <port>" host-initiated handshake, then
// exchange one length-prefixed JSON frame (microvm-guest-contract §1-3).
func vsockHealth(udsPath string) error {
	conn, err := net.DialTimeout("unix", udsPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte("CONNECT 5252\n")); err != nil {
		return err
	}
	// The VMM answers "OK <dst-cid>\n" once the guest accepts; it closes the
	// socket instead when the guest has no listener on the port.
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if len(line) > 32 || !strings.HasPrefix(line, "OK ") {
		return fmt.Errorf("unexpected vsock handshake: %q", line)
	}

	req, err := json.Marshal(map[string]any{
		"v":    1,
		"id":   "finalize-ready",
		"verb": "health",
		"args": map[string]any{},
	})
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(req)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := conn.Write(req); err != nil {
		return err
	}

	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > 1<<20 {
		return fmt.Errorf("bad response frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return err
	}
	var resp struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("health verb returned ok=false")
	}
	return nil
}
