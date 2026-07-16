// Package doctor diagnoses the Tier-2 setup for the active provider:
// credentials present, endpoint reachable / auth accepted, model present
// (when the backend can tell), JSON output working, and warm-call latency.
package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nudge/internal/config"
	"nudge/internal/llm"
	"nudge/internal/prompt"
	"nudge/internal/provider"
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
	if err != nil {
		fail("config: %v", err)
		return 1
	}

	prov, err := provider.Resolve(cfg)
	if err != nil {
		ui.Errf("  provider: %s\n", cfg.Provider)
		fail("%v", err)
		return 1
	}
	ui.Errf("  provider: %s, endpoint: %s, model: %s\n", prov.Name, prov.BaseURL, prov.Model)
	if prov.Name == "azure" {
		ui.Errf("  azure deployment: %s, api-version: %s\n", prov.AzureDeployment(), prov.AzureAPIVersion())
	}
	if prov.KeyEnv != "" {
		ui.Errf("  api key: %s\n", prov.KeyStatus())
	}
	if err := prov.KeyError(); err != nil {
		fail("%v", err)
		return 1
	}
	if prov.Cloud {
		ui.Errf("  %s\n", ui.Dim("note: this provider is a cloud service — tier-2 queries leave this machine"))
	}

	// Diagnostics get a generous timeout: locally the first call may load
	// the model from disk, which can far exceed the normal request budget.
	if prov.Timeout < 2*time.Minute {
		prov.Timeout = 2 * time.Minute
	}
	client := llm.New(cfg, prov)
	ctx := context.Background()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		fail("%s", explainErr(prov, err))
		return 1
	}
	pass("endpoint reachable%s", authNote(prov))

	if avail, known := client.ModelAvailable(ctx); known {
		if avail {
			pass("model %s is pulled", prov.Model)
		} else {
			fail("model %s not found — run `nudge setup` or `ollama pull %s`", prov.Model, prov.Model)
			return 1
		}
	} else {
		ui.Errf("  %s cannot list models on this backend; trying a live call\n", ui.Dim("--"))
	}

	// First call warms the model (may include load time locally), second
	// measures the warm latency users will actually feel.
	req := prompt.Request{Input: "git pshu", FixMode: true, Dir: "."}
	sp := ui.StartSpinner("first call...")
	t0 := time.Now()
	raw, err := client.Chat(ctx, prompt.System, req.User())
	warmup := time.Since(t0)
	sp.Stop()
	if err != nil {
		fail("%s", explainErr(prov, err))
		return 1
	}
	if _, perr := suggest.Parse(raw); perr != nil {
		fail("JSON output not working: %v", perr)
		ui.Errf("       raw output was: %.200s\n", raw)
		return 1
	}
	pass("JSON output working (first call %s%s)", warmup.Round(10*time.Millisecond), firstCallNote(prov))

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
		verdict = "over the ~2s budget"
		if !prov.Cloud {
			verdict += " — consider a smaller model or check CPU load"
		}
	}
	pass("warm call latency %s (%s)", warm.Round(10*time.Millisecond), verdict)

	if ok {
		ui.Errf("all good.\n")
		return 0
	}
	return 1
}

// explainErr turns raw HTTP/transport errors into provider-specific advice
// that names the actual problem. Keys are never included.
func explainErr(prov *provider.Provider, err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403") || strings.Contains(msg, "authentication failed"):
		return fmt.Sprintf("authentication failed — the key is present but %s rejected it; check %s", prov.Name, keyHint(prov))
	case prov.Name == "azure" && strings.Contains(msg, "HTTP 404"):
		return fmt.Sprintf("Azure returned 404 — deployment %q not found on %s (check azure_deployment, or api-version %s)",
			prov.AzureDeployment(), prov.BaseURL, prov.AzureAPIVersion())
	case strings.Contains(msg, "HTTP 404"):
		return fmt.Sprintf("%s returned 404 — model %q may not exist or the endpoint path is wrong: %v", prov.Name, prov.Model, err)
	case strings.Contains(msg, "HTTP 429"):
		return fmt.Sprintf("%s is rate-limiting or out of quota: %v", prov.Name, err)
	case strings.Contains(msg, "Client.Timeout") || strings.Contains(msg, "context deadline exceeded"):
		if prov.Cloud {
			return fmt.Sprintf("request to %s timed out: %v", prov.Name, err)
		}
		return fmt.Sprintf("request timed out — the model may still be loading, or the machine is under heavy load: %v", err)
	case isConnRefused(msg) && !prov.Cloud:
		return fmt.Sprintf("endpoint unreachable: %v\n       is your model server running? (Ollama: `ollama serve`, then `ollama pull %s`)", err, prov.Model)
	case isConnRefused(msg):
		return fmt.Sprintf("%s unreachable — check your network connection: %v", prov.Name, err)
	default:
		return fmt.Sprintf("request failed: %v", err)
	}
}

func isConnRefused(msg string) bool {
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connectex") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "context deadline exceeded")
}

func keyHint(prov *provider.Provider) string {
	if prov.KeyEnv != "" {
		return prov.KeyEnv
	}
	return "api_key"
}

func authNote(prov *provider.Provider) string {
	if prov.Cloud && prov.Name != "azure" {
		return ", key accepted"
	}
	return ""
}

func firstCallNote(prov *provider.Provider) string {
	if prov.Cloud {
		return ""
	}
	return ", includes model load"
}

func noteIfMissing(p string) string {
	if _, err := os.Stat(p); err != nil {
		return " (not present — using defaults)"
	}
	return ""
}
