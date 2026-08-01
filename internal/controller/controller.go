// Package controller runs a single task end-to-end: render prompt → sync repo →
// create feature worktree → export source → run the agent in an isolated
// container → apply the resulting bundle onto the feature branch → collect
// artifacts. This is the M3 slice; the state machine, worker pool, cancellation,
// and daemon integration are M5.
package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iQonAi/devbox/internal/agent"
	"github.com/iQonAi/devbox/internal/prompt"
	"github.com/iQonAi/devbox/internal/repo"
	"github.com/iQonAi/devbox/internal/runner"
)

// Terminal states (D9).
const (
	StateCompleted = "Completed"
	StateFailed    = "Failed"
)

// Limits are the per-run resource caps.
type Limits struct {
	CPUs      string
	MemoryMB  int
	PidsLimit int
	Timeout   time.Duration
}

// Deps are the collaborators a run needs.
type Deps struct {
	Repo   *repo.Manager
	Runner runner.Runner
	Image  string
}

// Request is one task to run.
type Request struct {
	TaskID        string
	Title         string // used for the feature-branch slug
	RepoName      string
	RepoURL       string
	DefaultBranch string
	TokenRef      string // mirror auth (M4); "" for local/public
	Prompt        prompt.Input
	Agent         agent.Agent
	AuthMethod    agent.AuthMethod
	AuthValue     string // model token/key value (M3: from flag/env; M5: LoadCredential)
	Limits        Limits
	WorkDir       string // host scratch dir for prompt, export, and out
}

// Artifact is a file a run produced, ready to be indexed by the caller.
type Artifact struct {
	Kind string
	Path string
}

// Outcome is the result of a run.
type Outcome struct {
	State     string
	Error     string // why a Failed task failed ("" when Completed)
	Commits   int
	ExitCode  int
	Branch    string
	Worktree  string
	OutDir    string
	Artifacts []Artifact
}

// artifactKinds maps a produced filename to its artifact kind, in a fixed order
// so collection is deterministic (stable CLI output and insertion order).
var artifactKinds = []struct{ name, kind string }{
	{"changes.bundle", "bundle"},
	{"transcript.json", "transcript"},
	{"diff.patch", "diff"},
	{"run.log", "log"},
	{"summary.txt", "summary"},
}

// Run executes the task. It returns an Outcome (with a terminal State) on a
// completed pipeline; it returns an error only when the pipeline itself could
// not run (a failing agent is a Failed Outcome, not an error).
func Run(ctx context.Context, deps Deps, req Request) (Outcome, error) {
	if req.Agent == nil {
		return Outcome{}, fmt.Errorf("no agent specified")
	}

	// 1. Render the prompt to a host file.
	promptText, err := prompt.Render(req.Prompt)
	if err != nil {
		return Outcome{}, err
	}
	promptPath := filepath.Join(req.WorkDir, "prompt.md")
	if err := os.MkdirAll(req.WorkDir, 0o755); err != nil {
		return Outcome{}, fmt.Errorf("create work dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(promptText), 0o644); err != nil {
		return Outcome{}, fmt.Errorf("write prompt: %w", err)
	}

	// 2. Sync the mirror and create the feature-branch worktree.
	mirror, err := deps.Repo.Sync(ctx, req.RepoName, req.RepoURL, req.TokenRef)
	if err != nil {
		return Outcome{}, err
	}
	branch := repo.BranchName(req.Agent.Name(), req.Title, req.TaskID)
	wt, err := deps.Repo.AddWorktree(ctx, mirror, req.TaskID, branch, req.DefaultBranch)
	if err != nil {
		return Outcome{}, err
	}

	// 3. Build the standalone source export handed to the container.
	exportDir := filepath.Join(req.WorkDir, "export")
	if err := deps.Repo.BuildExport(ctx, mirror, req.DefaultBranch, exportDir); err != nil {
		return Outcome{}, err
	}
	base, err := deps.Repo.ExportBase(ctx, exportDir)
	if err != nil {
		return Outcome{}, err
	}

	// 4. Compose the container command and the agent's env.
	envVar, err := req.Agent.EnvVar(req.AuthMethod)
	if err != nil {
		return Outcome{}, err
	}
	agentCmd, err := req.Agent.Command(req.AuthMethod, runner.PromptPath, runner.OutPath+"/transcript.json")
	if err != nil {
		return Outcome{}, err
	}

	// 5. Provide the model credential by env-file (value never in argv).
	secretEnv := map[string]string{}
	if req.AuthValue != "" {
		secretEnv[envVar] = req.AuthValue
	}

	outDir := filepath.Join(req.WorkDir, "out")
	spec := runner.Spec{
		Name:       req.TaskID,
		Image:      deps.Image,
		SourceDir:  exportDir,
		PromptFile: promptPath,
		OutDir:     outDir,
		SecretEnv:  secretEnv,
		Cmd:        wrapperCmd(base, agentCmd),
		CPUs:       req.Limits.CPUs,
		MemoryMB:   req.Limits.MemoryMB,
		PidsLimit:  req.Limits.PidsLimit,
	}

	// 6. Enforce the wall-clock timeout via the run context (§7.4).
	runCtx := ctx
	if req.Limits.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Limits.Timeout)
		defer cancel()
	}

	res, err := deps.Runner.Run(runCtx, spec)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			// The wall-clock timeout fired: a timed-out task is Failed (D9),
			// not a pipeline error.
			return Outcome{
				State:     StateFailed,
				Error:     "timeout",
				Branch:    branch,
				Worktree:  wt.Path,
				OutDir:    outDir,
				Artifacts: collectArtifacts(outDir),
			}, nil
		}
		return Outcome{}, fmt.Errorf("run agent container: %w", err)
	}

	out := Outcome{
		ExitCode: res.ExitCode,
		Branch:   branch,
		Worktree: wt.Path,
		OutDir:   outDir,
	}
	out.Artifacts = collectArtifacts(outDir)

	// 7. Apply the bundle onto the feature branch, if the agent produced commits.
	bundlePath := filepath.Join(outDir, "changes.bundle")
	if fi, statErr := os.Lstat(bundlePath); statErr == nil {
		if !fi.Mode().IsRegular() {
			// podman cp preserves symlinks, so a hostile agent could point
			// changes.bundle at an arbitrary host path; never follow it.
			out.State = StateFailed
			out.Error = "rejected non-regular artifact changes.bundle"
			return out, nil
		}
		applied, applyErr := deps.Repo.ApplyBundle(ctx, wt.Path, bundlePath)
		if applyErr != nil {
			// An unappliable bundle is a task failure, not a pipeline error.
			out.State = StateFailed
			out.Error = applyErr.Error()
			return out, nil
		}
		out.Commits = applied.Commits
	}

	// 8. Terminal outcome (D9): Completed iff the agent exited 0 AND ≥1 commit.
	if res.ExitCode == 0 && out.Commits >= 1 {
		out.State = StateCompleted
	} else {
		out.State = StateFailed
	}
	return out, nil
}

// collectArtifacts classifies the files a run left in outDir. Only regular
// files count: podman cp preserves symlinks, so a symlinked artifact could
// alias arbitrary host files (Lstat does not follow links).
func collectArtifacts(outDir string) []Artifact {
	var arts []Artifact
	for _, a := range artifactKinds {
		p := filepath.Join(outDir, a.name)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			arts = append(arts, Artifact{Kind: a.kind, Path: p})
		}
	}
	return arts
}
