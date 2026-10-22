//go:build linux

package tcpfirewall

import (
	"net"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// TestIsEgressAllowedDefaultDeny covers the default-deny egress posture
// (SANDBOX_DEFAULT_DENY_EGRESS): the fallback flips from allow to deny, and
// only an explicit allow rule (domain or CIDR) opens a path. Deny rules keep
// working unchanged.
func TestIsEgressAllowedDefaultDeny(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		network  *orchestrator.SandboxNetworkConfig
		hostname string
		ip       net.IP
		want     bool
	}{
		// The posture flips every historical default-allow case.
		{
			name:     "nil network config denies",
			network:  nil,
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     false,
		},
		{
			name:     "nil egress config denies",
			network:  &orchestrator.SandboxNetworkConfig{},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     false,
		},
		{
			name: "empty egress config denies",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     false,
		},
		{
			name: "unmatched destination denies",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					AllowedCidrs: []string{"10.0.0.0/8"},
				},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     false,
		},

		// Explicit allow rules still open paths.
		{
			name: "allowed CIDR match allows",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					AllowedCidrs: []string{"203.0.113.0/24"},
				},
			},
			hostname: noHostnameValue,
			ip:       net.ParseIP("203.0.113.7"),
			want:     true,
		},
		{
			name: "allowed domain match allows",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					AllowedDomains: []string{"target.example.com"},
				},
			},
			hostname: "target.example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true,
		},

		// Deny rules keep precedence over allow.
		{
			name: "denied CIDR still denies",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					DeniedCidrs: []string{"203.0.113.0/24"},
				},
			},
			hostname: noHostnameValue,
			ip:       net.ParseIP("203.0.113.7"),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sbx := &sandbox.Sandbox{
				Metadata: &sandbox.Metadata{
					Config: sandbox.NewConfig(sandbox.Config{Network: tt.network}),
				},
			}

			got, _, err := isEgressAllowed(sbx, tt.hostname, tt.ip, true)
			if err != nil {
				t.Fatalf("isEgressAllowed() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("isEgressAllowed(defaultDeny=true) = %v, want %v", got, tt.want)
			}
		})
	}
}
