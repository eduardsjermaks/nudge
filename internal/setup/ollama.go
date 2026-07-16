package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"nudge/internal/config"
	"nudge/internal/execx"
	"nudge/internal/ui"
)

// ensureOllama gets the local provider to a working state: server reachable,
// model pulled. Best-effort — failures are reported and left for the closing
// doctor run to diagnose in detail.
func ensureOllama(cfg config.Config) {
	if !serverUp(cfg.Endpoint) {
		if !startOrInstall(cfg) {
			ui.Errf("  skipping the model check — the server is not reachable.\n")
			return
		}
	} else {
		ui.Errf("  %s Ollama server reachable at %s\n", ui.Cyan("ok"), cfg.Endpoint)
	}

	models, err := listModels(cfg.Endpoint)
	if err != nil {
		ui.Errf("  cannot list models: %v\n", err)
		return
	}
	if hasModel(models, cfg.Model) {
		ui.Errf("  %s model %s is pulled\n", ui.Cyan("ok"), cfg.Model)
		return
	}

	ui.Errf("  model %s is not pulled yet (the default is a ~1 GB download).\n", ui.Bold(cfg.Model))
	yes, err := ui.AskYesNo("  Pull it now?", true)
	if err != nil || !yes {
		ui.Errf("  skipped — run `ollama pull %s` later.\n", cfg.Model)
		return
	}
	start := time.Now()
	err = pullModel(context.Background(), cfg.Endpoint, cfg.Model, renderProgress(cfg.Model))
	ui.Errf("\n")
	if err != nil {
		ui.Errf("  pull failed: %v\n", err)
		return
	}
	ui.Errf("  %s pulled %s in %s\n", ui.Cyan("ok"), cfg.Model, time.Since(start).Round(time.Second))
}

// startOrInstall handles a non-responding server: start it when the binary
// exists, otherwise offer the platform installer. Returns true when the
// server ends up reachable.
func startOrInstall(cfg config.Config) bool {
	if _, err := exec.LookPath("ollama"); err == nil {
		ui.Errf("  Ollama is installed but the server is not responding at %s.\n", cfg.Endpoint)
		yes, err := ui.AskYesNo("  Start it now (runs `ollama serve` in the background)?", true)
		if err != nil || !yes {
			return false
		}
		if err := startServer(); err != nil {
			ui.Errf("  failed to start the server: %v\n", err)
			return false
		}
		return waitUp(cfg.Endpoint, 20*time.Second)
	}

	cmd, note := installCommand(runtime.GOOS, hasBrew())
	if cmd == "" {
		ui.Errf("  Ollama is not installed. %s\n", note)
		return false
	}
	ui.Errf("  Ollama is not installed. nudge can run its installer for you:\n")
	ui.Errf("    %s\n", ui.Bold(cmd))
	if note != "" {
		ui.Errf("  %s\n", ui.Yellow(note))
	}
	yes, err := ui.AskYesNo("  Run it? (third-party installer — requires an explicit 'y')", false)
	if err != nil || !yes {
		ui.Errf("  skipped — install from https://ollama.com/download, then re-run `nudge setup`.\n")
		return false
	}
	if code := execx.Run(cmd); code != 0 {
		ui.Errf("  installer exited with code %d\n", code)
		return false
	}

	// This process's PATH was fixed at startup, so a fresh install may be
	// invisible here even though it succeeded (typical for winget).
	if _, err := exec.LookPath("ollama"); err != nil {
		if waitUp(cfg.Endpoint, 10*time.Second) {
			return true // the installer started a service; good enough
		}
		ui.Errf("  installed, but this terminal's PATH does not see it yet —\n")
		ui.Errf("  open a new terminal and run `nudge setup` again.\n")
		return false
	}
	if serverUp(cfg.Endpoint) {
		ui.Errf("  %s server is up\n", ui.Cyan("ok"))
		return true
	}
	if err := startServer(); err != nil {
		ui.Errf("  could not start the server: %v\n", err)
		return false
	}
	return waitUp(cfg.Endpoint, 20*time.Second)
}

// installCommand returns the install command for the platform, or "" plus a
// note when there is no runnable command. Pure so tests can cover the matrix.
func installCommand(goos string, brew bool) (cmd, note string) {
	switch goos {
	case "windows":
		return "winget install -e --id Ollama.Ollama", "installs the Ollama desktop app via winget"
	case "darwin":
		if brew {
			return "brew install ollama", ""
		}
		return "", "install it from https://ollama.com/download (no Homebrew found), then re-run `nudge setup`."
	default:
		return "curl -fsSL https://ollama.com/install.sh | sh", "Ollama's official installer — it may ask for your sudo password"
	}
}

func hasBrew() bool {
	_, err := exec.LookPath("brew")
	return err == nil
}

// startServer launches `ollama serve` detached, so it survives nudge exiting
// and ignores the terminal's Ctrl+C.
func startServer() error {
	cmd := exec.Command("ollama", "serve")
	cmd.SysProcAttr = detachAttrs()
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap if it dies while the wizard is still running
	return nil
}

func waitUp(endpoint string, max time.Duration) bool {
	sp := ui.StartSpinner("waiting for the server...")
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if serverUp(endpoint) {
			sp.Stop()
			ui.Errf("  %s server is up\n", ui.Cyan("ok"))
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	sp.Stop()
	ui.Errf("  the server did not come up within %s\n", max)
	return false
}

func serverUp(endpoint string) bool {
	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Get(endpoint + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func listModels(endpoint string) ([]string, error) {
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Get(endpoint + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

func hasModel(models []string, want string) bool {
	for _, m := range models {
		if m == want || m == want+":latest" {
			return true
		}
	}
	return false
}

// pullModel streams POST /api/pull, reporting each progress event. The HTTP
// client has no timeout — a ~1 GB pull takes as long as it takes; ctx is the
// only cancel.
func pullModel(ctx context.Context, endpoint, model string, onProgress func(status string, completed, total int64)) error {
	body, err := json.Marshal(map[string]any{"name": model, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("pull failed: HTTP %d: %s", resp.StatusCode, data)
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Status    string `json:"status"`
			Completed int64  `json:"completed"`
			Total     int64  `json:"total"`
			Error     string `json:"error"`
		}
		if err := dec.Decode(&m); err == io.EOF {
			return fmt.Errorf("pull stream ended without success")
		} else if err != nil {
			return err
		}
		if m.Error != "" {
			return fmt.Errorf("ollama: %s", m.Error)
		}
		if onProgress != nil {
			onProgress(m.Status, m.Completed, m.Total)
		}
		if m.Status == "success" {
			return nil
		}
	}
}

// renderProgress redraws one stderr line per event: a percentage while bytes
// are moving, the raw status otherwise.
func renderProgress(model string) func(status string, completed, total int64) {
	return func(status string, completed, total int64) {
		if total > 0 {
			ui.Errf("\r  pulling %s: %3.0f%% (%s / %s)          ", model,
				float64(completed)*100/float64(total), fmtBytes(completed), fmtBytes(total))
		} else {
			ui.Errf("\r  pulling %s: %-40s", model, status)
		}
	}
}

func fmtBytes(n int64) string {
	const (
		mb = 1 << 20
		gb = 1 << 30
	)
	if n >= gb {
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	}
	return fmt.Sprintf("%d MB", n/mb)
}
