package agent

import "fmt"

// pi is the Pi coding-agent adapter (npm @earendil-works/pi-coding-agent,
// binary `pi`). CLI contract researched from the pi repo docs:
// https://github.com/earendil-works/pi (packages/coding-agent/docs/usage.md,
// providers.md, json.md) and src/modes/print-mode.ts.
type piAgent struct{}

// Pi returns the Pi adapter.
func Pi() Agent { return piAgent{} }

func (piAgent) Name() string { return "pi" }

func (piAgent) EnvVar(method AuthMethod) (string, error) {
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

func (p piAgent) Command(method AuthMethod, promptPath, transcriptPath string) (string, error) {
	if _, err := p.EnvVar(method); err != nil {
		return "", err
	}
	// Notes:
	//   --provider anthropic: binds model selection to the ANTHROPIC_API_KEY
	//     injected for api_key auth; pi picks that provider's default model
	//     (defaultModelPerProvider, src/core/model-resolver.ts).
	//   --mode json: emits the session as JSON-lines events on stdout — the
	//     machine-readable transcript (docs/json.md). Non-interactive modes
	//     show no trust prompt and there are no per-tool permission prompts to
	//     skip (docs/usage.md).
	//   Prompt is a positional argument; pi has no prompt-file flag, so the
	//     wrapper substitutes the file content (docs/usage.md).
	//   jq guard: in --mode json pi exits 0 even when the model call fails —
	//     only text mode maps stopReason error/aborted to exit 1
	//     (src/modes/print-mode.ts) — so the run fails unless the transcript's
	//     last assistant message_end exists and did not stop on error/aborted.
	return fmt.Sprintf(
		`pi --provider anthropic --mode json "$(cat %[1]s)" > %[2]s && `+
			`jq -es 'map(select(.type == "message_end" and .message.role == "assistant")) | last as $m | `+
			`$m != null and (($m.message.stopReason // "") | IN("error", "aborted") | not)' %[2]s > /dev/null`,
		promptPath, transcriptPath,
	), nil
}
