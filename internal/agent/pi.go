package agent

import "fmt"

// pi is the Pi coding-agent adapter (npm @earendil-works/pi-coding-agent,
// binary `pi`). CLI contract researched from the pi repo docs:
// https://github.com/earendil-works/pi (packages/coding-agent/docs/usage.md,
// providers.md, json.md) and src/modes/print-mode.ts.
type pi struct{}

// Pi returns the Pi adapter.
func Pi() Agent { return pi{} }

func (pi) Name() string { return "pi" }

func (pi) EnvVar(method AuthMethod) (string, error) {
	switch method {
	case AuthAPIKey:
		// Pi is multi-provider and reads one API-key env var per provider
		// (docs/providers.md); ANTHROPIC_API_KEY is its Anthropic key. Devbox
		// runs pi against Anthropic — Command pins --provider anthropic so this
		// is the credential pi resolves.
		return "ANTHROPIC_API_KEY", nil
	default:
		// Pi's subscription auth is the interactive /login flow that stores
		// OAuth credentials in ~/.pi/agent/auth.json; no env var is documented
		// for injecting a subscription token, so only api_key is supported.
		return "", fmt.Errorf("pi: unsupported auth method %q", method)
	}
}

func (p pi) Command(method AuthMethod, promptPath, transcriptPath string) (string, error) {
	if _, err := p.EnvVar(method); err != nil {
		return "", err
	}
	// Notes:
	//   Prompt via stdin: piped stdin is read whole and becomes the one-shot
	//     initial message (src/cli/initial-message.ts, docs/usage.md). A bare
	//     positional would misparse prompts starting with '-' or '@' — pi's
	//     syntax is `pi [options] [@files...] [messages...]` with no documented
	//     `--` separator — so the prompt file is redirected in instead.
	//   --provider anthropic: a hint only — pi honors the CLI provider when
	//     --model is also given; with --provider alone it falls through its
	//     generic resolution chain. The run lands on Anthropic because
	//     ANTHROPIC_API_KEY, injected for api_key auth, is the only credential
	//     in the container (docs/providers.md).
	//   --mode json: emits the session as JSON-lines events on stdout — the
	//     machine-readable transcript (docs/json.md). Non-interactive modes
	//     show no trust prompt and there are no per-tool permission prompts to
	//     skip (docs/usage.md). NOTE: this JSONL shape is not what the
	//     controller's summary extraction expects (wrapper.go's
	//     `jq '.result // empty'` assumes claude's single-object transcript),
	//     so summary.txt stays empty for pi runs — follow-up in M6 hardening.
	//   jq guard: in --mode json pi exits 0 even when the model call fails —
	//     only text mode maps stopReason error/aborted to exit 1
	//     (src/modes/print-mode.ts) — so the run fails unless the transcript's
	//     last assistant message_end exists and did not stop on error/aborted.
	return fmt.Sprintf(
		`pi --provider anthropic --mode json < %[1]s > %[2]s && `+
			`jq -es 'map(select(.type == "message_end" and .message.role == "assistant")) | last as $m | `+
			`$m != null and (($m.message.stopReason // "") | IN("error", "aborted") | not)' %[2]s > /dev/null`,
		promptPath, transcriptPath,
	), nil
}
