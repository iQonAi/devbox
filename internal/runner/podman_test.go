package runner

import (
	"os"
	"slices"
	"strconv"
	"testing"
)

// argAfter returns the token following the first occurrence of flag, or "".
func argAfter(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func TestBuildCreateArgsHardening(t *testing.T) {
	args := buildCreateArgs(Spec{
		Image: "img", Cmd: []string{"echo", "hi"},
		CPUs: "2", MemoryMB: 2048, PidsLimit: 256,
	}, "vol-1", "")

	// Non-negotiable isolation flags must always be present.
	if argAfter(args, "--user") != containerUser {
		t.Errorf("--user = %q, want %q", argAfter(args, "--user"), containerUser)
	}
	if argAfter(args, "--cap-drop") != "ALL" {
		t.Error("missing --cap-drop ALL")
	}
	if argAfter(args, "--security-opt") != "no-new-privileges" {
		t.Error("missing --security-opt no-new-privileges")
	}
	if !slices.Contains(args, "--read-only") {
		t.Error("missing --read-only")
	}
	if argAfter(args, "--network") != networkMode {
		t.Error("wrong network mode")
	}
	if argAfter(args, "--dns") != dnsServer {
		t.Error("DNS not pinned to public resolver")
	}
	if argAfter(args, "-v") != "vol-1:"+taskMount {
		t.Errorf("task volume not mounted: %q", argAfter(args, "-v"))
	}
}

func TestBuildCreateArgsLimits(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img", CPUs: "2", MemoryMB: 2048, PidsLimit: 256}, "v", "")
	if argAfter(args, "--cpus") != "2" {
		t.Errorf("--cpus = %q", argAfter(args, "--cpus"))
	}
	if argAfter(args, "--memory") != "2048m" {
		t.Errorf("--memory = %q", argAfter(args, "--memory"))
	}
	if argAfter(args, "--pids-limit") != strconv.Itoa(256) {
		t.Errorf("--pids-limit = %q", argAfter(args, "--pids-limit"))
	}
}

// Unset limits must be omitted, not passed as zero (which Podman rejects).
func TestBuildCreateArgsOmitsUnsetLimits(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img"}, "v", "")
	for _, flag := range []string{"--cpus", "--memory", "--pids-limit", "--runtime"} {
		if slices.Contains(args, flag) {
			t.Errorf("%s should be omitted when unset", flag)
		}
	}
}

func TestBuildCreateArgsImageAndCmdLast(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img", Cmd: []string{"bash", "-c", "true"}}, "v", "")
	tail := args[len(args)-4:]
	if !slices.Equal(tail, []string{"img", "bash", "-c", "true"}) {
		t.Errorf("image+cmd not last: %v", tail)
	}
}

// Secret env is delivered via --env-file: only the file path appears in argv.
func TestBuildCreateArgsEnvFile(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img"}, "v", "/tmp/agent-env-xyz/env")
	if argAfter(args, "--env-file") != "/tmp/agent-env-xyz/env" {
		t.Errorf("env file not passed: %v", args)
	}
	// No secret file when none is provided.
	none := buildCreateArgs(Spec{Image: "img"}, "v", "")
	if slices.Contains(none, "--env-file") {
		t.Error("--env-file present with no secret env")
	}
}

// The runner writes name=value to a file so values never touch argv.
func TestWriteEnvFile(t *testing.T) {
	path, cleanup, err := writeEnvFile(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret-value"})
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	defer cleanup()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); got != "CLAUDE_CODE_OAUTH_TOKEN=secret-value\n" {
		t.Errorf("env file = %q", got)
	}
	// Empty map → no file, no-op cleanup.
	if p, _, _ := writeEnvFile(nil); p != "" {
		t.Errorf("expected empty path for no secret env, got %q", p)
	}
}

func TestBuildCreateArgsRuntimeSwap(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img", Runtime: "runsc"}, "v", "")
	if argAfter(args, "--runtime") != "runsc" {
		t.Error("runtime swap point not honored")
	}
}
