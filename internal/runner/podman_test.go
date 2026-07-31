package runner

import (
	"slices"
	"strconv"
	"strings"
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
	}, "vol-1")

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
	args := buildCreateArgs(Spec{Image: "img", CPUs: "2", MemoryMB: 2048, PidsLimit: 256}, "v")
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
	args := buildCreateArgs(Spec{Image: "img"}, "v")
	for _, flag := range []string{"--cpus", "--memory", "--pids-limit", "--runtime"} {
		if slices.Contains(args, flag) {
			t.Errorf("%s should be omitted when unset", flag)
		}
	}
}

func TestBuildCreateArgsImageAndCmdLast(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img", Cmd: []string{"bash", "-c", "true"}}, "v")
	tail := args[len(args)-4:]
	if !slices.Equal(tail, []string{"img", "bash", "-c", "true"}) {
		t.Errorf("image+cmd not last: %v", tail)
	}
}

// Env is forwarded by NAME only — never NAME=VALUE — so the value stays out of
// argv. Names are sorted for deterministic output.
func TestBuildCreateArgsPassEnvNamesOnly(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img", PassEnv: []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}}, "v")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-e ANTHROPIC_API_KEY -e CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("env not forwarded by sorted name: %q", joined)
	}
	// The forwarded names must never carry a value in argv.
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if strings.Contains(joined, name+"=") {
			t.Errorf("secret value leaked into argv for %s", name)
		}
	}
}

func TestBuildCreateArgsRuntimeSwap(t *testing.T) {
	args := buildCreateArgs(Spec{Image: "img", Runtime: "runsc"}, "v")
	if argAfter(args, "--runtime") != "runsc" {
		t.Error("runtime swap point not honored")
	}
}
