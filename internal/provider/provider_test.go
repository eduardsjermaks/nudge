package provider

import (
	"strings"
	"testing"
	"time"

	"nudge/internal/config"
)

func base(t *testing.T) config.Config {
	t.Helper()
	// Neutralize any real keys in the test environment.
	for _, k := range []string{"OPENAI_API_KEY", "AZURE_OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "NUDGE_API_KEY"} {
		t.Setenv(k, "")
	}
	return config.Defaults()
}

func TestPresets(t *testing.T) {
	cases := []struct {
		provider     string
		wantModel    string
		wantChatURL  string
		wantKeyEnv   string
		wantCloud    bool
		wantAuthHdr  string
	}{
		{"openai", "gpt-5-mini", "https://api.openai.com/v1/chat/completions", "OPENAI_API_KEY", true, "Authorization"},
		{"deepseek", "deepseek-chat", "https://api.deepseek.com/chat/completions", "DEEPSEEK_API_KEY", true, "Authorization"},
		{"anthropic", "claude-haiku-4-5", "https://api.anthropic.com/v1/messages", "ANTHROPIC_API_KEY", true, "x-api-key"},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			cfg := base(t)
			cfg.Provider = c.provider
			t.Setenv(c.wantKeyEnv, "test-key-1234")
			p, err := Resolve(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if p.Model != c.wantModel {
				t.Errorf("model = %q, want %q", p.Model, c.wantModel)
			}
			if p.ChatURL() != c.wantChatURL {
				t.Errorf("chat url = %q, want %q", p.ChatURL(), c.wantChatURL)
			}
			if p.Cloud != c.wantCloud {
				t.Errorf("cloud = %v", p.Cloud)
			}
			h := p.Headers()
			if _, ok := h[c.wantAuthHdr]; !ok {
				t.Errorf("missing %s header, got %v", c.wantAuthHdr, headerNames(h))
			}
			if p.Timeout != 8*time.Second {
				t.Errorf("cloud default timeout = %v, want 8s", p.Timeout)
			}
		})
	}
}

func TestOllamaDefault(t *testing.T) {
	cfg := base(t)
	t.Setenv("OPENAI_API_KEY", "sk-should-never-be-used")
	p, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "ollama" || p.Cloud {
		t.Fatalf("default provider = %+v, want local ollama", p)
	}
	// No silent fallback: a cloud key in the environment must not leak into
	// the local provider's requests.
	if p.APIKey != "" {
		t.Errorf("ollama provider picked up an API key from the environment")
	}
	if len(p.Headers()) != 0 {
		t.Errorf("ollama provider sends auth headers: %v", headerNames(p.Headers()))
	}
	if !strings.Contains(p.BaseURL, "localhost") {
		t.Errorf("ollama base URL = %q", p.BaseURL)
	}
	if p.Timeout != 30*time.Second {
		t.Errorf("local timeout = %v, want 30s", p.Timeout)
	}
}

func TestAzureURLAndValidation(t *testing.T) {
	cfg := base(t)
	cfg.Provider = "azure"
	t.Setenv("AZURE_OPENAI_API_KEY", "azkey9999")

	if _, err := Resolve(cfg); err == nil || !strings.Contains(err.Error(), "azure_endpoint") {
		t.Errorf("missing endpoint: err = %v, want mention of azure_endpoint", err)
	}
	cfg.AzureEndpoint = "https://myres.openai.azure.com"
	if _, err := Resolve(cfg); err == nil || !strings.Contains(err.Error(), "azure_deployment") {
		t.Errorf("missing deployment: err = %v, want mention of azure_deployment", err)
	}
	cfg.AzureDeployment = "my-deploy"
	p, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://myres.openai.azure.com/openai/deployments/my-deploy/chat/completions?api-version=2024-10-21"
	if p.ChatURL() != want {
		t.Errorf("azure chat url:\n got  %q\n want %q", p.ChatURL(), want)
	}
	if p.Headers()["api-key"] != "azkey9999" {
		t.Errorf("azure must auth via api-key header, got %v", headerNames(p.Headers()))
	}
	if _, ok := p.Headers()["Authorization"]; ok {
		t.Errorf("azure must not send a Bearer header")
	}
	cfg.AzureAPIVersion = "2025-01-01"
	p, _ = Resolve(cfg)
	if !strings.HasSuffix(p.ChatURL(), "api-version=2025-01-01") {
		t.Errorf("api-version override ignored: %q", p.ChatURL())
	}
}

func TestKeyResolutionOrder(t *testing.T) {
	cfg := base(t)
	cfg.Provider = "openai"

	// 3rd priority: plaintext api_key in the config file.
	cfg.APIKey = "from-file"
	p, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.APIKey != "from-file" {
		t.Errorf("plaintext key: got %q", p.APIKey)
	}

	// 2nd priority: api_key_env indirection beats the file.
	t.Setenv("MY_KEY_VAR", "from-indirect")
	cfg.APIKeyEnv = "MY_KEY_VAR"
	p, _ = Resolve(cfg)
	if p.APIKey != "from-indirect" {
		t.Errorf("api_key_env: got %q", p.APIKey)
	}

	// 1st priority: the standard env var beats both.
	t.Setenv("OPENAI_API_KEY", "from-standard-env")
	p, _ = Resolve(cfg)
	if p.APIKey != "from-standard-env" {
		t.Errorf("standard env: got %q", p.APIKey)
	}

	// api_key_env naming an unset var is a config error, not a silent miss.
	cfg2 := base(t)
	cfg2.Provider = "openai"
	cfg2.APIKeyEnv = "DOES_NOT_EXIST_XYZ"
	if _, err := Resolve(cfg2); err == nil || !strings.Contains(err.Error(), "DOES_NOT_EXIST_XYZ") {
		t.Errorf("unset api_key_env: err = %v", err)
	}
}

func TestMissingKeyError(t *testing.T) {
	cfg := base(t)
	cfg.Provider = "openai"
	p, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.KeyError(); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("KeyError = %v, want mention of OPENAI_API_KEY", err)
	}
	// custom works keyless (a local LM Studio box needs none).
	cfg.Provider = "custom"
	p, _ = Resolve(cfg)
	if err := p.KeyError(); err != nil {
		t.Errorf("custom without key should be fine, got %v", err)
	}
}

func TestModelOverride(t *testing.T) {
	cfg := base(t)
	cfg.Provider = "openai"
	cfg.Model = "gpt-5.4-mini"
	cfg.ModelSet = true
	t.Setenv("OPENAI_API_KEY", "k")
	p, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Model != "gpt-5.4-mini" {
		t.Errorf("model override ignored: %q", p.Model)
	}
}

func TestKeyStatusNeverRevealsKey(t *testing.T) {
	cfg := base(t)
	cfg.Provider = "openai"
	t.Setenv("OPENAI_API_KEY", "sk-verysecretkeyvalue")
	p, _ := Resolve(cfg)
	st := p.KeyStatus()
	if strings.Contains(st, "verysecret") {
		t.Errorf("KeyStatus leaks the key: %q", st)
	}
	if !strings.Contains(st, "alue") {
		t.Errorf("KeyStatus should show the last 4 chars: %q", st)
	}
}

func TestUnknownProvider(t *testing.T) {
	cfg := base(t)
	cfg.Provider = "gemini"
	if _, err := Resolve(cfg); err == nil || !strings.Contains(err.Error(), "gemini") {
		t.Errorf("unknown provider: err = %v", err)
	}
}

func headerNames(h map[string]string) []string {
	var out []string
	for k := range h {
		out = append(out, k)
	}
	return out
}
