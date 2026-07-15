package llm

// Live integration tests, one per cloud provider. Each skips cleanly when
// its key is absent, so `go test ./...` stays green on machines (and CI)
// without credentials. Each sends one real correction request and checks it
// survives the same validation the binary applies.

import (
	"context"
	"os"
	"testing"

	"nudge/internal/config"
	"nudge/internal/prompt"
	"nudge/internal/provider"
	"nudge/internal/suggest"
)

func liveTest(t *testing.T, providerName, keyEnv string, mutate func(*config.Config)) {
	t.Helper()
	if os.Getenv(keyEnv) == "" {
		t.Skipf("%s not set — skipping %s integration test", keyEnv, providerName)
	}
	cfg := config.Defaults()
	cfg.Provider = providerName
	if mutate != nil {
		mutate(&cfg)
	}
	prov, err := provider.Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := prov.KeyError(); err != nil {
		t.Skipf("%v", err)
	}
	client := New(cfg, prov)
	req := prompt.Request{Input: "git pshu", FixMode: true, Dir: t.TempDir()}
	raw, err := client.Chat(context.Background(), prompt.System, req.User())
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	s, err := suggest.Parse(raw)
	if err != nil {
		t.Fatalf("model output failed validation: %v\nraw: %.300s", err, raw)
	}
	if s.Command != "git push" {
		t.Errorf("suggestion = %q, want %q (confidence %.2f)", s.Command, "git push", s.Confidence)
	}
	t.Logf("%s (%s) → %q, confidence %.2f", providerName, prov.Model, s.Command, s.Confidence)
}

func TestOpenAIIntegration(t *testing.T) {
	liveTest(t, "openai", "OPENAI_API_KEY", nil)
}

func TestDeepSeekIntegration(t *testing.T) {
	liveTest(t, "deepseek", "DEEPSEEK_API_KEY", nil)
}

func TestAnthropicIntegration(t *testing.T) {
	liveTest(t, "anthropic", "ANTHROPIC_API_KEY", nil)
}

func TestAzureIntegration(t *testing.T) {
	if os.Getenv("AZURE_OPENAI_ENDPOINT") == "" || os.Getenv("AZURE_OPENAI_DEPLOYMENT") == "" {
		t.Skip("AZURE_OPENAI_ENDPOINT / AZURE_OPENAI_DEPLOYMENT not set — skipping azure integration test")
	}
	liveTest(t, "azure", "AZURE_OPENAI_API_KEY", func(c *config.Config) {
		c.AzureEndpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
		c.AzureDeployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT")
	})
}
