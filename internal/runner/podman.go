package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

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

// commander runs one podman invocation and reports its exit code. A non-zero
// exit is returned in code, NOT as err — err means the command could not be run
// or was killed by the context. This split matters: a container that exits 1 is
// a valid Result, not a runner failure.
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
	if e.env != nil {
		cmd.Env = e.env
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), ee.ExitCode(), nil // ran; non-zero exit is not our error
		}
		return out.String(), -1, fmt.Errorf("podman %s: %w: %s", strings.Join(args, " "), err, errb.String())
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

// Run executes the full lifecycle: fresh volume → create → copy source in →
// start → copy artifacts out → destroy. The container and volume are always
// torn down, even on error or cancellation (disposability, §8.9).
func (r *PodmanRunner) Run(ctx context.Context, spec Spec) (Result, error) {
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

	// Run. start -a exits with the container command's status.
	_, code, err := r.cmd.run(ctx, "start", "-a", name)
	if err != nil {
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
