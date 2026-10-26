//go:build linux

package fc

import (
	"bytes"
	"fmt"
	"os"

	"path/filepath"
	txtTemplate "text/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/artifact"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// startScriptArgs represents the arguments for the start script template
type startScriptArgs struct {
	SandboxDir string

	HostKernelPath    string
	SandboxKernelDir  string
	SandboxKernelFile string

	HostRootfsPath             string
	DeprecatedSandboxRootfsDir string
	SandboxRootfsFile          string

	NamespaceID       string
	FirecrackerPath   string
	FirecrackerSocket string

	// VsockPrivateDir, when set, is a per-sandbox host directory that the
	// start script bind-mounts over VsockDir inside the Firecracker process's
	// private mount namespace (NIL-211): a restored snapshot binds its vsock
	// UDS at the path baked at build time, so without this every restore of
	// the same template would collide on one socket file.
	VsockPrivateDir string
	VsockDir        string
}

// StartScriptResult contains the generated script and computed paths
type StartScriptResult struct {
	// Value is the generated firecracker start script
	Value string

	// RootfsPath is the computed rootfs path (with backward compatibility)
	RootfsPath string

	// KernelPath is the computed kernel path
	KernelPath string

	// VsockPrivateDir is this sandbox's private vsock directory (the
	// bind-mount source), empty when SANDBOX_VSOCK_DIR is unset.
	VsockPrivateDir string
	// VsockOwnerMarker names the generation marker file inside
	// VsockPrivateDir; Stop removes the directory only while this marker
	// still exists (NIL-211 cross-generation same-ID race).
	VsockOwnerMarker string
}

const startScriptV1 = `mount --make-rprivate / &&
{{ if .VsockPrivateDir }}mount --bind {{ .VsockPrivateDir }} {{ .VsockDir }} &&
{{ end }}
mount -t tmpfs tmpfs {{ .DeprecatedSandboxRootfsDir }} -o X-mount.mkdir &&
ln -s {{ .HostRootfsPath }} {{ .DeprecatedSandboxRootfsDir }}/{{ .SandboxRootfsFile }} &&

mount -t tmpfs tmpfs {{ .SandboxDir }}/{{ .SandboxKernelDir }} -o X-mount.mkdir &&
ln -s {{ .HostKernelPath }} {{ .SandboxDir }}/{{ .SandboxKernelDir }}/{{ .SandboxKernelFile }} &&

ip netns exec {{ .NamespaceID }} {{ .FirecrackerPath }} --api-sock {{ .FirecrackerSocket }}`

const startScriptV2 = `mount --make-rprivate / &&
{{ if .VsockPrivateDir }}mount --bind {{ .VsockPrivateDir }} {{ .VsockDir }} &&
{{ end }}mount -t tmpfs tmpfs {{ .SandboxDir }} -o X-mount.mkdir &&

ln -s {{ .HostRootfsPath }} {{ .SandboxDir }}/{{ .SandboxRootfsFile }} &&

mkdir -p {{ .SandboxDir }}/{{ .SandboxKernelDir }} &&
ln -s {{ .HostKernelPath }} {{ .SandboxDir }}/{{ .SandboxKernelDir }}/{{ .SandboxKernelFile }} &&

ip netns exec {{ .NamespaceID }} {{ .FirecrackerPath }} --api-sock {{ .FirecrackerSocket }}`

// StartScriptBuilder handles the creation and execution of firecracker start scripts
type StartScriptBuilder struct {
	builderConfig cfg.BuilderConfig
	templateV1    *txtTemplate.Template
	templateV2    *txtTemplate.Template
}

// NewStartScriptBuilder creates a new StartScriptBuilder instance
func NewStartScriptBuilder(builderConfig cfg.BuilderConfig) *StartScriptBuilder {
	templateV1 := txtTemplate.Must(txtTemplate.New("fc-start-v1").Parse(startScriptV1))
	templateV2 := txtTemplate.Must(txtTemplate.New("fc-start-v2").Parse(startScriptV2))

	return &StartScriptBuilder{
		builderConfig: builderConfig,
		templateV1:    templateV1,
		templateV2:    templateV2,
	}
}

// buildArgs prepares the arguments for the start script template
func (sb *StartScriptBuilder) buildArgs(
	versions Config,
	files *storage.SandboxFiles,
	rootfsPaths RootfsPaths,
	namespaceID string,
) startScriptArgs {
	args := startScriptArgs{
		// General
		SandboxDir: sb.builderConfig.SandboxDir,

		// Kernel
		HostKernelPath:    versions.HostKernelPath(sb.builderConfig),
		SandboxKernelDir:  versions.SandboxKernelDir(),
		SandboxKernelFile: artifact.KernelFileName,

		// Rootfs
		HostRootfsPath:             files.SandboxCacheRootfsLinkPath(sb.builderConfig.StorageConfig),
		DeprecatedSandboxRootfsDir: rootfsPaths.DeprecatedSandboxRootfsDir(),
		SandboxRootfsFile:          artifact.RootfsFileName,

		// FC
		NamespaceID:       namespaceID,
		FirecrackerPath:   versions.FirecrackerPath(sb.builderConfig),
		FirecrackerSocket: files.SandboxFirecrackerSocketPath(),
	}

	return args
}

// preparePrivateVsockDir creates this sandbox's private vsock directory with
// a generation marker file inside it. The marker lets a later Process.Stop
// remove only the directory its own generation created: a new sandbox that
// reuses the same ID recreates the directory with a NEW marker, so a slow
// teardown of the old generation must not delete the new one's live UDS.
// Fail-loud: a node that cannot create the directory cannot give the
// sandbox an isolated vsock view, and silently falling back would re-open
// the exact shared-path collision this exists to fix.
func preparePrivateVsockDir(files *storage.SandboxFiles) (privateDir, ownerMarker string, err error) {
	dir := os.Getenv("SANDBOX_VSOCK_DIR")
	if dir == "" {
		return "", "", nil
	}
	private := filepath.Join(dir, files.SandboxID)
	if err := os.MkdirAll(private, 0o755); err != nil {
		return "", "", fmt.Errorf("creating private vsock dir: %w", err)
	}
	marker, err := os.CreateTemp(private, ".owner-*")
	if err != nil {
		return "", "", fmt.Errorf("creating vsock dir owner marker: %w", err)
	}
	name := marker.Name()
	if err := marker.Close(); err != nil {
		return "", "", fmt.Errorf("closing vsock dir owner marker: %w", err)
	}
	return private, filepath.Base(name), nil
}

// GenerateScript builds and executes the start script template with the provided arguments
func (sb *StartScriptBuilder) GenerateScript(args startScriptArgs, rootfsPaths RootfsPaths) (string, error) {
	var scriptBuffer bytes.Buffer

	// Choose the appropriate template based on the rootfs version
	var template *txtTemplate.Template
	if rootfsPaths.TemplateVersion <= 1 {
		template = sb.templateV1
	} else {
		template = sb.templateV2
	}

	err := template.Execute(&scriptBuffer, args)
	if err != nil {
		return "", fmt.Errorf("error executing fc start script template: %w", err)
	}

	return scriptBuffer.String(), nil
}

// Build creates a complete StartScriptResult with script, args, and computed paths
func (sb *StartScriptBuilder) Build(
	versions Config,
	files *storage.SandboxFiles,
	rootfsPaths RootfsPaths,
	namespaceID string,
) (*StartScriptResult, error) {
	args := sb.buildArgs(versions, files, rootfsPaths, namespaceID)

	privateDir, ownerMarker, err := preparePrivateVsockDir(files)
	if err != nil {
		return nil, err
	}
	args.VsockPrivateDir = privateDir
	args.VsockDir = os.Getenv("SANDBOX_VSOCK_DIR")

	script, err := sb.GenerateScript(args, rootfsPaths)
	if err != nil {
		_ = os.RemoveAll(privateDir)
		return nil, err
	}

	rootfsPath := sb.getRootfsPath(args, rootfsPaths)
	kernelPath := sb.getKernelPath(args)

	return &StartScriptResult{
		Value:      script,
		RootfsPath: rootfsPath,
		KernelPath: kernelPath,

		// VsockPrivateDir/VsockOwnerMarker (empty when the node does not run
		// the NilOps agent channel): the private bind-mount source and the
		// generation marker guarding its removal. Stop removes the directory
		// only while its own marker is still there.
		VsockPrivateDir:  privateDir,
		VsockOwnerMarker: ownerMarker,
	}, nil
}

// getRootfsPath returns the rootfs path based on the script args, with backward compatibility
func (sb *StartScriptBuilder) getRootfsPath(args startScriptArgs, rootfsPaths RootfsPaths) string {
	rootfsPath := filepath.Join(args.SandboxDir, args.SandboxRootfsFile)
	if rootfsPaths.TemplateVersion <= 1 {
		rootfsPath = filepath.Join(args.DeprecatedSandboxRootfsDir, args.SandboxRootfsFile)
	}

	return rootfsPath
}

// getKernelPath returns the kernel path based on the script args
func (sb *StartScriptBuilder) getKernelPath(args startScriptArgs) string {
	return filepath.Join(args.SandboxDir, args.SandboxKernelDir, args.SandboxKernelFile)
}
