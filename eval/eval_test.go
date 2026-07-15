// Package eval runs the labeled model-quality suite against a real local
// model. It is skipped unless NUDGE_EVAL=1 and the model server is
// reachable, so `go test ./...` stays green on machines without a model.
//
// cases.json is test data (expected outputs for grading), not matching
// configuration — nudge itself never reads it.
package eval

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"nudge/internal/config"
	"nudge/internal/llm"
	"nudge/internal/prompt"
	"nudge/internal/provider"
	"nudge/internal/suggest"
)

//go:embed cases.json
var casesJSON []byte

type evalCase struct {
	Name       string   `json:"name"`
	Input      string   `json:"input"`
	Fix        bool     `json:"fix"`
	Exit       int      `json:"exit"`
	Markers    []string `json:"markers"`
	Accept     []string `json:"accept"`
	ExpectNone bool     `json:"expect_none"`
}

const passRateRequired = 0.80

func TestModelEval(t *testing.T) {
	if os.Getenv("NUDGE_EVAL") != "1" {
		t.Skip("set NUDGE_EVAL=1 (with a local model running) to run the model eval")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	// NUDGE_PROVIDER / NUDGE_MODEL env overrides let this same case set
	// score any configured backend, e.g. NUDGE_PROVIDER=openai.
	prov, err := provider.Resolve(cfg)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if err := prov.KeyError(); err != nil {
		t.Skipf("provider %s: %v", prov.Name, err)
	}
	client := llm.New(cfg, prov)
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Skipf("provider %s not reachable at %s: %v", prov.Name, prov.BaseURL, err)
	}
	fmt.Printf("\neval provider: %s, model: %s\n", prov.Name, prov.Model)

	var cases []evalCase
	if err := json.Unmarshal(casesJSON, &cases); err != nil {
		t.Fatalf("cases.json: %v", err)
	}

	// Warm the model so per-case latencies reflect steady state.
	warm := prompt.Request{Input: "git pshu", FixMode: true, Dir: t.TempDir()}
	_, _ = client.Chat(ctx, prompt.System, warm.User())

	pass := 0
	var totalWarm time.Duration
	fmt.Printf("\n%-26s %-6s %8s  %s\n", "case", "result", "latency", "got")
	for _, c := range cases {
		dir := t.TempDir()
		for _, m := range c.Markers {
			if err := os.WriteFile(filepath.Join(dir, m), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		req := prompt.Request{Input: c.Input, FixMode: c.Fix, ExitCode: c.Exit, Dir: dir}

		t0 := time.Now()
		s, gotNone := ask(ctx, client, cfg, req)
		took := time.Since(t0)
		totalWarm += took

		ok, got := grade(c, s, gotNone)
		if ok {
			pass++
		}
		res := "PASS"
		if !ok {
			res = "FAIL"
		}
		fmt.Printf("%-26s %-6s %7dms  %s\n", c.Name, res, took.Milliseconds(), got)
	}

	rate := float64(pass) / float64(len(cases))
	fmt.Printf("\npass rate: %d/%d = %.0f%% (required >= %.0f%%), mean warm latency %dms\n",
		pass, len(cases), rate*100, passRateRequired*100, (totalWarm / time.Duration(len(cases))).Milliseconds())
	if rate < passRateRequired {
		t.Errorf("pass rate %.0f%% below required %.0f%%", rate*100, passRateRequired*100)
	}
}

// ask mirrors the binary's tier-2 path: chat, validate, one retry.
func ask(ctx context.Context, client llm.Client, cfg config.Config, req prompt.Request) (*suggest.Suggestion, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := client.Chat(ctx, prompt.System, req.User())
		if err != nil {
			return nil, true
		}
		s, err := suggest.Parse(raw)
		if err != nil {
			continue
		}
		if s.Command == "" || s.Confidence == 0 {
			return nil, true // model explicitly declined — that's an answer
		}
		return s, false
	}
	return nil, true
}

func grade(c evalCase, s *suggest.Suggestion, gotNone bool) (bool, string) {
	if c.ExpectNone {
		if gotNone {
			return true, "(no suggestion, as expected)"
		}
		return false, fmt.Sprintf("expected no suggestion, got %q", s.Command)
	}
	if gotNone {
		return false, "(no suggestion)"
	}
	got := strings.Join(strings.Fields(s.Command), " ") // normalize whitespace
	for _, pat := range c.Accept {
		if regexp.MustCompile(pat).MatchString(got) {
			return true, got
		}
	}
	return false, got + "  [wanted: " + strings.Join(c.Accept, " | ") + "]"
}
