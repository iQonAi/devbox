package agent

// Guard fixture tests (#37): run each adapter's real Command snippet via
// `bash -c` against committed fixture transcripts, with a stub agent binary
// on PATH, and pin the load-bearing clause of the adapter contract —
// "Command must exit non-zero when the agent fails". For pi that clause is
// the jq guard (pi's json mode exits 0 even on model failure); for claude it
// is plain exit-status propagation past the best-effort summary extraction.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stubScript is the fake agent binary installed on PATH under the adapter's
// name. It ignores its arguments and stdin, emits the fixture transcript on
// stdout (which the snippet redirects to transcriptPath), and exits the
// scripted status — enough to drive every branch of the real snippet.
const stubScript = `#!/bin/sh
cat -- "$STUB_TRANSCRIPT" || exit 97
exit "${STUB_EXIT:-0}"
`

// requireGuardTools skips when bash or jq is missing: the snippets need
// both, and environments without them must skip cleanly, not fail.
func requireGuardTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH; guard snippets need it", tool)
		}
	}
}

// runGuardSnippet builds a's real Command snippet against paths in a temp
// dir — Command takes promptPath and transcriptPath as arguments, and the
// summary lands next to the transcript via summaryPath — installs the stub
// agent, runs the snippet via `bash -c`, and returns the snippet's exit code
// plus the summary.txt contents (trailing newline trimmed).
func runGuardSnippet(t *testing.T, a Agent, fixture string, stubExit int) (int, string) {
	t.Helper()

	fixtureAbs, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatalf("abs %s: %v", fixture, err)
	}

	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")
	binDir := filepath.Join(dir, "bin")
	for _, d := range []string{outDir, binDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	promptPath := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("fixture prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, a.Name()), []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	transcriptPath := filepath.Join(outDir, "transcript.json")
	snippet, err := a.Command(AuthAPIKey, promptPath, transcriptPath)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	// Rebuild the environment with the stub dir first on PATH, dropping the
	// inherited PATH entry so the override wins regardless of which duplicate
	// the child resolves.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_TRANSCRIPT="+fixtureAbs,
		"STUB_EXIT="+strconv.Itoa(stubExit),
	)

	cmd := exec.Command("bash", "-c", snippet)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("bash -c: %v\n%s", err, out)
		}
		exitCode = exitErr.ExitCode()
	}

	summary, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read summary: %v", err)
	}
	return exitCode, strings.TrimSuffix(string(summary), "\n")
}

type guardCase struct {
	name     string
	fixture  string
	stubExit int
	wantExit int // -1 means any non-zero exit
	// wantSummary is asserted only when checkSummary: on failure fixtures
	// the summary is best-effort by contract, never a test failure itself.
	checkSummary bool
	wantSummary  string
}

func runGuardCases(t *testing.T, a Agent, cases []guardCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exitCode, summary := runGuardSnippet(t, a, tc.fixture, tc.stubExit)
			if tc.wantExit == -1 {
				if exitCode == 0 {
					t.Errorf("exit = 0, want non-zero (fixture %s)", tc.fixture)
				}
			} else if exitCode != tc.wantExit {
				t.Errorf("exit = %d, want %d (fixture %s)", exitCode, tc.wantExit, tc.fixture)
			}
			if tc.checkSummary && summary != tc.wantSummary {
				t.Errorf("summary = %q, want %q", summary, tc.wantSummary)
			}
		})
	}
}

// TestPiGuardFixtures pins pi's jq guard: with the stub exiting 0 (as pi's
// json mode does even on model failure), only the transcript's last assistant
// message_end decides pass/fail. ok variants exit 0 with the summary
// extracted (string content, and block-array content with thinking blocks
// dropped); error/aborted stopReason, an empty transcript, a truncated JSONL
// line, and a transcript with no assistant message_end all exit non-zero;
// and a non-zero pi exit propagates as-is past guard and extraction.
func TestPiGuardFixtures(t *testing.T) {
	requireGuardTools(t)
	runGuardCases(t, Pi(), []guardCase{
		{
			name: "ok", fixture: "testdata/pi/ok.jsonl", wantExit: 0,
			checkSummary: true, wantSummary: "Added /healthz endpoint returning 200.",
		},
		{
			name: "ok-blocks", fixture: "testdata/pi/ok-blocks.jsonl", wantExit: 0,
			checkSummary: true, wantSummary: "Implemented the fix.\nAll tests pass.",
		},
		{name: "error", fixture: "testdata/pi/error.jsonl", wantExit: -1},
		{name: "aborted", fixture: "testdata/pi/aborted.jsonl", wantExit: -1},
		{name: "empty", fixture: "testdata/pi/empty.jsonl", wantExit: -1},
		{name: "truncated", fixture: "testdata/pi/truncated.jsonl", wantExit: -1},
		{name: "missing-assistant", fixture: "testdata/pi/missing-assistant.jsonl", wantExit: -1},
		{name: "agent-exit-propagates", fixture: "testdata/pi/ok.jsonl", stubExit: 3, wantExit: 3},
	})
}

// TestClaudeGuardFixtures pins claude's exit path: claude's own exit code
// carries failure, so the contract is that the stub's status survives the
// summary extraction unchanged — including when extraction succeeds on a good
// transcript — and that .result lands in summary.txt (empty when absent).
func TestClaudeGuardFixtures(t *testing.T) {
	requireGuardTools(t)
	runGuardCases(t, Claude(), []guardCase{
		{
			name: "ok", fixture: "testdata/claude/ok.json", wantExit: 0,
			checkSummary: true, wantSummary: "Added the endpoint and updated tests.",
		},
		{
			// Extraction succeeds on the good transcript, yet the snippet must
			// still re-raise the agent's failure.
			name: "exit-propagates-past-extraction", fixture: "testdata/claude/ok.json",
			stubExit: 7, wantExit: 7,
			checkSummary: true, wantSummary: "Added the endpoint and updated tests.",
		},
		{
			// No .result in the error shape: summary is deterministically empty.
			name: "error-no-result", fixture: "testdata/claude/error.json",
			stubExit: 1, wantExit: 1,
			checkSummary: true, wantSummary: "",
		},
	})
}
