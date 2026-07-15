// Package provider resolves the active Tier-2 model provider: which wire
// protocol to speak, base URL, default model, timeout, and where the API
// credential comes from.
//
// Exactly one provider is active at a time, selected in config. There is no
// fallback chain, and in particular no silent escalation to cloud: a
// credential found in the environment is never used unless that provider was
// explicitly selected. If the local server is down, nudge degrades to Tier 1.
package provider

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"sync"
	"time"

	"nudge/internal/config"
)

// Protocol is the wire protocol a provider speaks. Two protocols serve all
// providers: OpenAI-compatible chat completions (openai, azure, deepseek,
// custom) and the Anthropic Messages API. Ollama keeps its native API.
type Protocol int

const (
	ProtoOllama Protocol = iota
	ProtoOpenAI
	ProtoAnthropic
)

const cloudTimeout = 8 * time.Second

// Provider is the fully resolved active provider.
type Provider struct {
	Name     string
	Protocol Protocol
	BaseURL  string // base for building URLs; endpoint host for azure
	Model    string
	Cloud    bool   // requests leave the machine; secret masking applies
	KeyEnv   string // standard env var for the credential ("" = none used)
	APIKey   string // resolved credential ("" when absent)
	Timeout  time.Duration

	azureDeployment string
	azureAPIVersion string
}

// Resolve maps the config to a concrete provider. It validates
// provider-specific requirements with messages that name what is missing.
func Resolve(cfg config.Config) (*Provider, error) {
	p := &Provider{Name: cfg.Provider, Timeout: timeout(cfg, true)}
	switch cfg.Provider {
	case "", "ollama":
		p.Name = "ollama"
		p.Protocol = ProtoOllama
		p.BaseURL = cfg.Endpoint
		p.Model = cfg.Model
		p.Timeout = timeout(cfg, false)
		return p, nil
	case "custom":
		p.Protocol = ProtoOpenAI
		p.BaseURL = cfg.Endpoint
		p.Model = cfg.Model
		p.KeyEnv = "NUDGE_API_KEY"
		p.Timeout = timeout(cfg, false)
	case "openai":
		p.Protocol = ProtoOpenAI
		p.BaseURL = "https://api.openai.com"
		p.Model = modelOr(cfg, "gpt-5-mini")
		p.Cloud = true
		p.KeyEnv = "OPENAI_API_KEY"
	case "deepseek":
		p.Protocol = ProtoOpenAI
		p.BaseURL = "https://api.deepseek.com"
		p.Model = modelOr(cfg, "deepseek-chat")
		p.Cloud = true
		p.KeyEnv = "DEEPSEEK_API_KEY"
	case "anthropic":
		p.Protocol = ProtoAnthropic
		p.BaseURL = "https://api.anthropic.com"
		p.Model = modelOr(cfg, "claude-haiku-4-5")
		p.Cloud = true
		p.KeyEnv = "ANTHROPIC_API_KEY"
	case "azure":
		p.Protocol = ProtoOpenAI
		p.Cloud = true
		p.KeyEnv = "AZURE_OPENAI_API_KEY"
		if cfg.AzureEndpoint == "" {
			return nil, fmt.Errorf("provider azure: azure_endpoint is not set — add azure_endpoint = \"https://<resource>.openai.azure.com\" to %s", config.Path())
		}
		if cfg.AzureDeployment == "" {
			return nil, fmt.Errorf("provider azure: azure_deployment is not set — add azure_deployment = \"<your deployment name>\" to %s", config.Path())
		}
		p.BaseURL = cfg.AzureEndpoint
		p.azureDeployment = cfg.AzureDeployment
		p.azureAPIVersion = cfg.AzureAPIVersion
		if p.azureAPIVersion == "" {
			p.azureAPIVersion = "2024-10-21"
		}
		// Azure's "model" is the deployment; a model override only affects
		// the request body, which Azure ignores in favor of the path.
		p.Model = modelOr(cfg, cfg.AzureDeployment)
	default:
		return nil, fmt.Errorf("unknown provider %q — valid values: ollama, openai, azure, anthropic, deepseek, custom", cfg.Provider)
	}
	key, err := resolveKey(cfg, p.KeyEnv)
	if err != nil {
		return nil, err
	}
	p.APIKey = key
	return p, nil
}

// KeyError reports a missing credential for providers that require one.
// custom and ollama work without a key.
func (p *Provider) KeyError() error {
	if p.Cloud && p.APIKey == "" {
		return fmt.Errorf("provider %s: no API key — set %s (or api_key_env / api_key in %s)", p.Name, p.KeyEnv, config.Path())
	}
	return nil
}

// ChatURL returns the full URL for one chat/completion request.
func (p *Provider) ChatURL() string {
	switch {
	case p.Name == "azure":
		return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
			p.BaseURL, url.PathEscape(p.azureDeployment), url.QueryEscape(p.azureAPIVersion))
	case p.Protocol == ProtoAnthropic:
		return p.BaseURL + "/v1/messages"
	case p.Name == "deepseek":
		return p.BaseURL + "/chat/completions"
	default: // openai, custom
		return p.BaseURL + "/v1/chat/completions"
	}
}

// PingURL returns a cheap authenticated GET target, when the provider has
// one. Azure's data plane has no cheap GET — doctor does a live call instead.
func (p *Provider) PingURL() (string, bool) {
	switch {
	case p.Name == "azure":
		return "", false
	case p.Protocol == ProtoAnthropic:
		return p.BaseURL + "/v1/models", true
	case p.Name == "deepseek":
		return p.BaseURL + "/models", true
	default:
		return p.BaseURL + "/v1/models", true
	}
}

// Headers returns the auth (and protocol) headers for every request.
// Never log these.
func (p *Provider) Headers() map[string]string {
	h := map[string]string{}
	switch {
	case p.Name == "azure":
		h["api-key"] = p.APIKey
	case p.Protocol == ProtoAnthropic:
		h["x-api-key"] = p.APIKey
		h["anthropic-version"] = "2023-06-01"
	case p.APIKey != "":
		h["Authorization"] = "Bearer " + p.APIKey
	}
	return h
}

// KeyStatus describes the credential for the doctor report without
// revealing it: "present, ends …xxxx" or "not set".
func (p *Provider) KeyStatus() string {
	if p.APIKey == "" {
		return "not set"
	}
	tail := p.APIKey
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return "present, ends …" + tail
}

// AzureDeployment returns the deployment name (azure only, else "").
func (p *Provider) AzureDeployment() string { return p.azureDeployment }

// AzureAPIVersion returns the api-version in use (azure only, else "").
func (p *Provider) AzureAPIVersion() string { return p.azureAPIVersion }

func modelOr(cfg config.Config, def string) string {
	if cfg.ModelSet && cfg.Model != "" {
		return cfg.Model
	}
	return def
}

func timeout(cfg config.Config, cloud bool) time.Duration {
	if cfg.TimeoutMS > 0 {
		return time.Duration(cfg.TimeoutMS) * time.Millisecond
	}
	if cloud {
		return cloudTimeout
	}
	return time.Duration(cfg.TimeoutSec) * time.Second
}

var plaintextWarnOnce sync.Once

// resolveKey finds the credential, in priority order: the provider's
// standard env var, then the env var named by api_key_env, then plaintext
// api_key in the config file (discouraged: warned once, and the file is
// tightened to 0600 on POSIX).
func resolveKey(cfg config.Config, keyEnv string) (string, error) {
	if keyEnv != "" {
		if v := os.Getenv(keyEnv); v != "" {
			return v, nil
		}
	}
	if cfg.APIKeyEnv != "" {
		v := os.Getenv(cfg.APIKeyEnv)
		if v == "" {
			return "", fmt.Errorf("api_key_env names %q but that variable is empty or unset", cfg.APIKeyEnv)
		}
		return v, nil
	}
	if cfg.APIKey != "" {
		plaintextWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "nudge: warning: plaintext api_key in %s — prefer the %s env var or api_key_env\n", config.Path(), keyEnv)
			if runtime.GOOS != "windows" {
				_ = os.Chmod(config.Path(), 0o600)
			}
		})
		return cfg.APIKey, nil
	}
	return "", nil
}
