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

// stubFailExit is the stub's own internal-failure exit code (fixture missing
// or unreadable). runGuardSnippet turns it into a hard test failure, so a
// broken fixture path can never satisfy a non-zero expectation. Guard cases
// must never use it as stubExit.
const stubFailExit = 97

// stubScript is the fake agent binary installed on PATH under the adapter's
// name. It ignores its arguments and stdin, emits the fixture transcript on
// stdout (which the snippet redirects to transcriptPath), and exits the
// scripted status — enough to drive every branch of the real snippet. Plain
// `cat` (no `--`): the fixture path is absolute and never dash-prefixed, and
// `cat --` is not POSIX (breaks BSD/macOS). Keep the exit code in sync with
// stubFailExit.
const stubScript = `#!/bin/sh
if ! cat "$STUB_TRANSCRIPT"; then
	echo "stub: cannot read fixture: $STUB_TRANSCRIPT" >&2
	exit 97
fi
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

// guardResult is one snippet run's observable outcome.
type guardResult struct {
	exitCode      int
	summary       string // summary.txt contents, trailing newline trimmed
	summaryExists bool   // false when summary.txt was never written
	output        string // snippet's combined stdout+stderr, for failure messages
}

// runGuardSnippet builds a's real Command snippet against paths in a temp
// dir — Command takes promptPath and transcriptPath as arguments, and the
// summary lands next to the transcript via summaryPath — installs the stub
// agent, runs the snippet via `bash -c`, and returns the run's outcome. A
// stubFailExit exit is a hard test failure: it means the stub could not read
// the fixture, not that the guard fired.
func runGuardSnippet(t *testing.T, a Agent, fixture string, stubExit int) guardResult {
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
	if exitCode == stubFailExit {
		t.Fatalf("stub could not read fixture %s (exit %d) — broken test setup, not a guard verdict:\n%s",
			fixture, stubFailExit, out)
	}

	summary, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	summaryExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read summary: %v", err)
	}
	return guardResult{
		exitCode:      exitCode,
		summary:       strings.TrimSuffix(string(summary), "\n"),
		summaryExists: summaryExists,
		output:        string(out),
	}
}

type guardCase struct {
	name     string
	fixture  string
	stubExit int
	// wantExit is the exact expected exit code. -1 means any non-zero, and is
	// reserved for cases whose exact code is tool-version-sensitive (jq's
	// parse-error code); stubFailExit can never satisfy it, since
	// runGuardSnippet hard-fails on that code first.
	wantExit int
	// wantSummary is asserted only when checkSummary: summary.txt must then
	// exist and hold exactly wantSummary — "" means written-but-empty, never
	// absent. On failure fixtures the summary is best-effort by contract, so
	// those cases leave checkSummary unset.
	checkSummary bool
	wantSummary  string
}

func runGuardCases(t *testing.T, a Agent, cases []guardCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runGuardSnippet(t, a, tc.fixture, tc.stubExit)
			if tc.wantExit == -1 {
				if res.exitCode == 0 {
					t.Errorf("exit = 0, want non-zero (fixture %s)\nsnippet output:\n%s",
						tc.fixture, res.output)
				}
			} else if res.exitCode != tc.wantExit {
				t.Errorf("exit = %d, want %d (fixture %s)\nsnippet output:\n%s",
					res.exitCode, tc.wantExit, tc.fixture, res.output)
			}
			if tc.checkSummary {
				if !res.summaryExists {
					t.Errorf("summary.txt not written, want contents %q (fixture %s)\nsnippet output:\n%s",
						tc.wantSummary, tc.fixture, res.output)
				} else if res.summary != tc.wantSummary {
					t.Errorf("summary = %q, want %q\nsnippet output:\n%s",
						res.summary, tc.wantSummary, res.output)
				}
			}
		})
	}
}

// guardCases maps each adapter name in the adapters registry (agent.go) to
// its guard fixture table. TestGuardRegistryParity carries parity_test.go's
// convention over to this suite: registering a new agent fails it until the
// agent gets a table here or a documented exemption in guardExempt.
var guardCases = map[string][]guardCase{
	// pi's jq guard: with the stub exiting 0 (as pi's json mode does even on
	// model failure), only the transcript's last assistant message_end decides
	// pass/fail. ok variants exit 0 with the summary extracted (string
	// content, and block-array content with thinking blocks dropped);
	// error/aborted stopReason, an empty transcript, and a transcript with no
	// assistant message_end exit exactly 1 — jq -e's stable false/null verdict
	// — while a truncated JSONL line exits non-zero via jq's parse error,
	// whose code is version-sensitive; and a non-zero pi exit propagates as-is
	// past guard and extraction.
	"pi": {
		{
			name: "ok", fixture: "testdata/pi/ok.jsonl", wantExit: 0,
			checkSummary: true, wantSummary: "Added /healthz endpoint returning 200.",
		},
		{
			name: "ok-blocks", fixture: "testdata/pi/ok-blocks.jsonl", wantExit: 0,
			checkSummary: true, wantSummary: "Implemented the fix.\nAll tests pass.",
		},
		{name: "error", fixture: "testdata/pi/error.jsonl", wantExit: 1},
		{name: "aborted", fixture: "testdata/pi/aborted.jsonl", wantExit: 1},
		{name: "empty", fixture: "testdata/pi/empty.jsonl", wantExit: 1},
		{name: "truncated", fixture: "testdata/pi/truncated.jsonl", wantExit: -1},
		{name: "missing-assistant", fixture: "testdata/pi/missing-assistant.jsonl", wantExit: 1},
		{name: "agent-exit-propagates", fixture: "testdata/pi/ok.jsonl", stubExit: 3, wantExit: 3},
	},
	// claude's exit path: claude's own exit code carries failure, so the
	// contract is that the stub's status survives the summary extraction
	// unchanged — including when extraction succeeds on a good transcript —
	// and that .result lands in summary.txt (written empty when absent).
	"claude": {
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
			// No .result in the error shape: extraction still writes
			// summary.txt, deterministically empty.
			name: "error-no-result", fixture: "testdata/claude/error.json",
			stubExit: 1, wantExit: 1,
			checkSummary: true, wantSummary: "",
		},
	},
}

// guardExempt lists registered adapters exempt from guard fixture coverage,
// each with a reason. mock: its Command invokes no agent binary — it writes
// files and commits via git — so the stub-on-PATH mechanism has nothing to
// stand in for.
var guardExempt = map[string]bool{
	"mock": true,
}

// TestGuardRegistryParity mirrors the covered check in parity_test.go:49-57
// for the exit-non-zero clause: every adapter in the adapters registry must
// have at least one guard fixture case, or an explicit exemption above.
func TestGuardRegistryParity(t *testing.T) {
	for name := range adapters {
		if len(guardCases[name]) == 0 && !guardExempt[name] {
			t.Errorf("registered agent %q has no guard fixture case in this suite; add a guardCases table or a documented guardExempt entry", name)
		}
	}
}

func TestPiGuardFixtures(t *testing.T) {
	requireGuardTools(t)
	runGuardCases(t, Pi(), guardCases["pi"])
}

func TestClaudeGuardFixtures(t *testing.T) {
	requireGuardTools(t)
	runGuardCases(t, Claude(), guardCases["claude"])
}
