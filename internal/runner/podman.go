package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// validName constrains a task name: it becomes the container/volume name and is
// embedded in `podman cp` targets, so only safe characters are allowed.
var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// buildCreateArgs returns the `podman create …` arguments for a spec, with every
// isolation control applied. volume is the per-task named volume mounted at
// /task. This is pure and deterministic (flags in a fixed order) so it can be
// asserted in tests without invoking Podman.
func buildCreateArgs(spec Spec, volume string) []string {
	args := []string{
		"create",
		"--name", ContainerName(spec),
		"--user", containerUser,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--read-only",
		"--tmpfs", tmpfsTmp,
		"-e", "HOME=" + taskMount,
		"-v", volume + ":" + taskMount,
		"--network", networkMode,
		"--dns", dnsServer,
	}

	// Runtime swap point: empty means the engine default (crun); "runsc" flips
	// on gVisor after #17 with no other change.
	if spec.Runtime != "" {
		args = append(args, "--runtime", spec.Runtime)
	}
	if spec.CPUs != "" {
		args = append(args, "--cpus", spec.CPUs)
	}
	if spec.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.MemoryMB))
	}
	if spec.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(spec.PidsLimit))
	}

	// Image and command come last, in that order.
	args = append(args, spec.Image)
	args = append(args, spec.Cmd...)
	return args
}

// ContainerName is the deterministic per-task container/volume name, so a
// crashed run's leftovers can be found and cleaned by name.
func ContainerName(spec Spec) string {
	return "agent-task-" + spec.Name
}

// commander runs one podman invocation. On any failure it returns a non-nil err
// (carrying stderr); code is the process exit status, or -1 if it could not run.
// Most callers just check err; `start` is the exception — a non-zero container
// exit is a valid Result, so it keys off code and ignores a non-negative-code err.
type commander interface {
	run(ctx context.Context, args ...string) (stdout string, code int, err error)
}

// execCommander runs the real podman via a configurable base command, so the
// cross-user hop to agentbox is injected rather than hardcoded.
type execCommander struct {
	base []string // e.g. ["podman"] or ["sudo","-u","agentbox","/usr/local/sbin/agentbox-podman"]
	env  []string // nil = inherit the parent environment
}

func (e execCommander) run(ctx context.Context, args ...string) (string, int, error) {
	full := append(append([]string{}, e.base...), args...)
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	// Run from a neutral, world-traversable dir: the cross-user hop inherits the
	// caller's cwd, and agentbox cannot chdir into the daemon's home or /opt/devbox.
	// Every path we pass podman is absolute, so cwd is irrelevant to correctness.
	cmd.Dir = "/"
	if e.env != nil {
		cmd.Env = e.env
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Ran but exited non-zero: report the code AND an error carrying
			// stderr. Callers that treat a non-zero exit as legitimate (only
			// `start`, for the container's own status) key off code, not err.
			return out.String(), ee.ExitCode(),
				fmt.Errorf("podman %s: exit %d: %s", strings.Join(args, " "), ee.ExitCode(), strings.TrimSpace(errb.String()))
		}
		return out.String(), -1, fmt.Errorf("podman %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), 0, nil
}

// PodmanRunner runs task containers via rootless Podman.
type PodmanRunner struct {
	cmd commander
}

// NewPodmanRunner builds a runner. base is the command that accepts podman args
// (["podman"] for dev; the sudo→agentbox wrapper in production). env nil inherits.
func NewPodmanRunner(base, env []string) *PodmanRunner {
	if len(base) == 0 {
		base = []string{"podman"}
	}
	return &PodmanRunner{cmd: execCommander{base: base, env: env}}
}

// seedOutDir creates an empty /task/out inside the container by copying an empty
// host dir named "out" into /task. The seed dir is world-accessible because in
// production agentbox (not this process) reads it during the copy.
func (r *PodmanRunner) seedOutDir(ctx context.Context, name string) error {
	parent, err := os.MkdirTemp("", "agent-seed-")
	if err != nil {
		return fmt.Errorf("seed out dir: %w", err)
	}
	defer os.RemoveAll(parent)

	out := filepath.Join(parent, "out")
	if err := os.Mkdir(out, 0o755); err != nil {
		return fmt.Errorf("seed out dir: %w", err)
	}
	// agentbox reads (traverses) these during the copy; it needs r-x, not write.
	// MkdirTemp makes parent 0700, so widen it to let agentbox in.
	if err := os.Chmod(parent, 0o755); err != nil {
		return fmt.Errorf("seed out dir perms: %w", err)
	}

	if _, _, err := r.cmd.run(ctx, "cp", out, name+":"+taskMount); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	return nil
}

// Run executes the full lifecycle: fresh volume → create → copy source in →
// start → copy artifacts out → destroy. The container and volume are always
// torn down, even on error or cancellation (disposability, §8.9).
func (r *PodmanRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	// Name is embedded into the container/volume name and into `podman cp`
	// targets (name:/path); reject anything that could break that parsing.
	if !validName.MatchString(spec.Name) {
		return Result{}, fmt.Errorf("invalid task name %q: must match %s", spec.Name, validName)
	}

	name := ContainerName(spec)
	volume := name

	// Clear any leftovers from a prior crashed run with the same name.
	r.cmd.run(ctx, "rm", "-f", name)
	r.cmd.run(ctx, "volume", "rm", "-f", volume)

	if _, _, err := r.cmd.run(ctx, "volume", "create", volume); err != nil {
		return Result{}, fmt.Errorf("create volume: %w", err)
	}

	// Teardown uses a fresh context: it must run even when ctx was cancelled.
	defer func() {
		r.cmd.run(context.Background(), "rm", "-f", name)
		r.cmd.run(context.Background(), "volume", "rm", "-f", volume)
	}()

	if _, _, err := r.cmd.run(ctx, buildCreateArgs(spec, volume)...); err != nil {
		return Result{}, fmt.Errorf("create container: %w", err)
	}

	// Copy source IN — Podman remaps ownership to the container uid.
	if _, _, err := r.cmd.run(ctx, "cp", spec.SourceDir+"/.", name+":"+SrcPath); err != nil {
		return Result{}, fmt.Errorf("copy source in: %w", err)
	}

	// Guarantee the drop-dir exists so the command can write artifacts and so
	// collection is meaningful even if the command creates nothing — podman cp of
	// a missing dir is a silent no-op, which would otherwise swallow that failure.
	if err := r.seedOutDir(ctx, name); err != nil {
		return Result{}, err
	}

	// Run. start -a exits with the container command's status, so a non-zero
	// exit (code >= 0) is a valid Result — only a negative code (podman itself
	// failed to run/observe the container) is a runner error.
	_, code, err := r.cmd.run(ctx, "start", "-a", name)
	if code < 0 {
		return Result{}, fmt.Errorf("start container: %w", err)
	}

	// Collect artifacts OUT as inert data — even on a non-zero container exit,
	// since partial output (a transcript, logs) is still worth keeping.
	if err := os.MkdirAll(spec.OutDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("create out dir: %w", err)
	}
	if _, _, err := r.cmd.run(ctx, "cp", name+":"+OutPath+"/.", spec.OutDir); err != nil {
		return Result{}, fmt.Errorf("copy artifacts out: %w", err)
	}

	return Result{ExitCode: code}, nil
}
