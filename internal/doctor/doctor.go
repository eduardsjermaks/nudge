// Package doctor diagnoses the Tier-2 setup: endpoint reachable, model
// present, JSON mode working, and warm-call latency.
package doctor

import (
	"context"
	"fmt"
	"os"
	"time"

	"nudge/internal/config"
	"nudge/internal/llm"
	"nudge/internal/prompt"
	"nudge/internal/suggest"
	"nudge/internal/ui"
)

// Run performs all checks and returns a process exit code.
func Run() int {
	ok := true
	pass := func(f string, a ...any) { ui.Errf("  %s %s\n", ui.Cyan("ok"), fmt.Sprintf(f, a...)) }
	fail := func(f string, a ...any) { ui.Errf("  %s %s\n", ui.Red("FAIL"), fmt.Sprintf(f, a...)); ok = false }

	cfg, err := config.Load()
	ui.Errf("nudge doctor\n")
	ui.Errf("  config file: %s%s\n", config.Path(), noteIfMissing(config.Path()))
	ui.Errf("  backend: %s, endpoint: %s, model: %s\n", cfg.Backend, cfg.Endpoint, cfg.Model)
	if err != nil {
		fail("config: %v", err)
		return 1
	}

	client := llm.New(cfg)
	ctx := context.Background()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		fail("endpoint unreachable: %v", err)
		ui.Errf("       is your model server running? (Ollama: `ollama serve`, then `ollama pull %s`)\n", cfg.Model)
		return 1
	}
	pass("endpoint reachable")

	if avail, known := client.ModelAvailable(ctx); known {
		if avail {
			pass("model %s is pulled", cfg.Model)
		} else {
			fail("model %s not found — run `ollama pull %s`", cfg.Model, cfg.Model)
			return 1
		}
	} else {
		ui.Errf("  %s cannot list models on this backend; trying a live call\n", ui.Dim("--"))
	}

	// First call warms the model (may include load time), second measures
	// the warm latency users will actually feel.
	req := prompt.Request{Input: "git pshu", FixMode: true, Dir: "."}
	sp := ui.StartSpinner("warming model...")
	t0 := time.Now()
	raw, err := client.Chat(ctx, prompt.System, req.User())
	warmup := time.Since(t0)
	sp.Stop()
	if err != nil {
		fail("generation failed: %v", err)
		return 1
	}
	if _, perr := suggest.Parse(raw); perr != nil {
		fail("JSON mode not working: %v", perr)
		ui.Errf("       raw output was: %.200s\n", raw)
		return 1
	}
	pass("JSON mode working (first call %s, includes model load)", warmup.Round(10*time.Millisecond))

	sp = ui.StartSpinner("measuring warm latency...")
	t0 = time.Now()
	raw, err = client.Chat(ctx, prompt.System, req.User())
	warm := time.Since(t0)
	sp.Stop()
	if err != nil {
		fail("warm call failed: %v", err)
		return 1
	}
	if _, perr := suggest.Parse(raw); perr != nil {
		fail("warm call returned invalid JSON: %v", perr)
		return 1
	}
	verdict := "within the ~2s budget"
	if warm > 2*time.Second {
		verdict = "over the ~2s budget — consider a smaller model or check CPU load"
	}
	pass("warm call latency %s (%s)", warm.Round(10*time.Millisecond), verdict)

	if ok {
		ui.Errf("all good.\n")
		return 0
	}
	return 1
}

func noteIfMissing(p string) string {
	if _, err := os.Stat(p); err != nil {
		return " (not present — using defaults)"
	}
	return ""
}
