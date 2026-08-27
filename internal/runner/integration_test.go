//go:build integration

// These tests run the real PodmanRunner against a live rootless Podman as
// agentbox. They are excluded from the default build and only run on the VM:
//
//	RUNNER_PODMAN_BASE="sudo -u agentbox /usr/local/sbin/agentbox-podman" \
//	  go test -tags integration ./internal/runner -v
//
// RUNNER_IMAGE overrides the base image (default localhost/agent-task-base:dev).
package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iQonAi/agent-task/internal/gitx"
)

func podmanBase() []string {
	if v := os.Getenv("RUNNER_PODMAN_BASE"); v != "" {
		return strings.Fields(v)
	}
	return []string{"podman"}
}

func image() string {
	if v := os.Getenv("RUNNER_IMAGE"); v != "" {
		return v
	}
	return "localhost/agent-task-base:dev"
}

// openDir returns a world-accessible temp dir. t.TempDir() is 0700 and owned by
// the test user, so agentbox (the container/cp uid) could not read the source
// copy or write the artifacts; 0777 on a non-sticky dir lets the test user still
// clean up agentbox-owned files afterward.
func openDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "m2-itest-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	// Sticky (01777): agentbox can create files during the copy, but other
	// users cannot delete/rename files they do not own on this multi-user VM.
	if err := os.Chmod(d, 0o1777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	if _, err := gitx.Run(ctx, dir, "init", "-b", "main"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, a := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		if _, err := gitx.Run(ctx, dir, a...); err != nil {
			t.Fatalf("git %v: %v", a, err)
		}
	}
}

// TestIntegrationRoundTrip is the M2 gate: source copied in, a commit made
// inside the hardened container, a bundle written out, and the host verifying
// that bundle as inert data.
func TestIntegrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := openDir(t)
	initGitRepo(t, src)
	base, err := gitx.Run(ctx, src, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	out := openDir(t)

	spec := Spec{
		Name: "m2-roundtrip", Image: image(), SourceDir: src, OutDir: out,
		CPUs: "2", MemoryMB: 2048, PidsLimit: 256,
		Cmd: []string{"bash", "-lc", fmt.Sprintf(
			`set -e
			 mkdir -p %[2]s
			 cd %[1]s
			 git config user.email agent@localhost
			 git config user.name  agent
			 echo "agent was here" > agent.txt
			 git add agent.txt
			 git commit -qm "agent commit"
			 git bundle create %[2]s/changes.bundle %[3]s..HEAD`,
			SrcPath, OutPath, base)},
	}

	res, err := NewPodmanRunner(podmanBase(), nil).Run(ctx, spec)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("container exit = %d, want 0", res.ExitCode)
	}

	bundle := filepath.Join(out, "changes.bundle")
	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("bundle not collected: %v", err)
	}
	// Host reads the bundle as inert data — verifies, never executes.
	if _, err := gitx.Run(ctx, src, "bundle", "verify", bundle); err != nil {
		t.Fatalf("bundle verify failed: %v", err)
	}
}

// TestIntegrationEgress proves the deny-list holds from inside a hardened
// container: the public internet is reachable, an RFC1918 address is not.
func TestIntegrationEgress(t *testing.T) {
	src := openDir(t)
	initGitRepo(t, src)
	out := openDir(t)

	spec := Spec{
		Name: "m2-egress", Image: image(), SourceDir: src, OutDir: out,
		Cmd: []string{"bash", "-lc",
			`curl -s -m 8 -o /dev/null -w "%{http_code}" https://icanhazip.com > /task/out/net.txt || echo curlfail > /task/out/net.txt
			 echo >> /task/out/net.txt
			 curl -s -m 5 -o /dev/null http://10.0.0.1 && echo REACHED >> /task/out/net.txt || echo blocked >> /task/out/net.txt`},
	}

	if _, err := NewPodmanRunner(podmanBase(), nil).Run(context.Background(), spec); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "net.txt"))
	if err != nil {
		t.Fatalf("read net.txt: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "200") {
		t.Errorf("public internet not reachable from container: %q", s)
	}
	if !strings.Contains(s, "blocked") {
		t.Errorf("RFC1918 10.0.0.1 was not blocked: %q", s)
	}
}

// TestIntegrationMemoryLimitEnforced proves --memory is actually enforced:
// allocating far beyond the cap must be OOM-killed (non-zero exit), not swapped
// through. Relies on the cgroup memory controller being delegated to agentbox.
func TestIntegrationMemoryLimitEnforced(t *testing.T) {
	src := openDir(t)
	initGitRepo(t, src)
	out := openDir(t)

	spec := Spec{
		Name: "m2-mem", Image: image(), SourceDir: src, OutDir: out,
		MemoryMB: 128,
		Cmd:      []string{"bash", "-lc", `python3 -c "x = bytearray(1024*1024*1024); print(len(x))"`},
	}

	res, err := NewPodmanRunner(podmanBase(), nil).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Error("allocating 1GB under a 128MB cap exited 0 — memory limit not enforced")
	}
}
