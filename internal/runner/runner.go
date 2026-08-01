// Package runner launches disposable, isolated containers for agent tasks
// (rootless Podman; gVisor runtime deferred to #17 but swappable via Spec.Runtime).
// Every isolation control is enforced here, not left to callers.
package runner

import "context"

// Fixed isolation invariants. These are not configurable: a caller must not be
// able to weaken the sandbox.
const (
	containerUser = "10001"           // non-root agent user baked into the base image
	taskMount     = "/task"           // writable per-task volume (source copy + out dir)
	SrcPath       = "/task/src"       // where the source copy lands, in-container
	OutPath       = "/task/out"       // where the agent writes artifacts, in-container
	PromptPath    = "/task/prompt.md" // the rendered prompt, in-container
	dnsServer     = "9.9.9.9"         // public resolver; internal DNS lives in denied ranges
	networkMode   = "slirp4netns"
	tmpfsTmp      = "/tmp:rw,size=64m"
)

// Spec describes one task container. Only task-specific fields — the hardening
// is applied unconditionally by the runner.
type Spec struct {
	Name       string            // task id; forms the container/volume name (safe chars only)
	Image      string            // e.g. localhost/devbox-agent-base:dev
	Cmd        []string          // command run inside; writes artifacts to OutPath
	SourceDir  string            // host dir (a git repo) copied into SrcPath
	PromptFile string            // host file copied read-only to /task/prompt.md; "" = none
	OutDir     string            // host dir where artifacts are collected OUT (must be runner-writable)
	SecretEnv  map[string]string // name→value; written to an --env-file so the value
	//                             (the model key) never appears in argv
	CPUs      string // e.g. "2"; empty = unset
	MemoryMB  int    // e.g. 2048; 0 = unset
	PidsLimit int    // e.g. 256; 0 = unset
	Runtime   string // OCI runtime; "" = engine default, "runsc" once #17 lands
}

// Result is the outcome of a container run.
type Result struct {
	ExitCode int // the container command's exit status
}

// Runner launches a task container and collects its artifacts. One interface so
// the engine/runtime is swappable (D5) without touching the controller.
type Runner interface {
	Run(ctx context.Context, spec Spec) (Result, error)
}
