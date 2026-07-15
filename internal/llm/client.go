// Package llm talks to the model server of the active provider. Two wire
// protocols cover every provider: OpenAI-compatible chat completions
// (OpenAI, Azure, DeepSeek, custom local servers) and the Anthropic Messages
// API. Ollama keeps its native API for keep_alive and JSON mode.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"nudge/internal/config"
	"nudge/internal/provider"
)

// Client is the one interface the rest of nudge sees.
type Client interface {
	// Chat sends a system+user prompt and returns the raw model text.
	Chat(ctx context.Context, system, user string) (string, error)
	// Ping verifies the server is reachable and, for cloud providers, that
	// the credential is accepted (cheap, no generation).
	Ping(ctx context.Context) error
	// ModelAvailable reports whether the configured model is present, when
	// the backend can tell; ok=false means "can't tell".
	ModelAvailable(ctx context.Context) (available bool, ok bool)
}

func New(cfg config.Config, prov *provider.Provider) Client {
	hc := &http.Client{Timeout: prov.Timeout}
	switch prov.Protocol {
	case provider.ProtoAnthropic:
		return &anthropicClient{prov: prov, hc: hc}
	case provider.ProtoOpenAI:
		return &openaiClient{prov: prov, hc: hc}
	default:
		return &ollamaClient{cfg: cfg, hc: hc}
	}
}

// --- Ollama native ---

type ollamaClient struct {
	cfg config.Config
	hc  *http.Client
}

func (c *ollamaClient) Chat(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream":     false,
		"format":     "json",
		"keep_alive": c.cfg.KeepAlive,
		"options": map[string]any{
			"temperature": 0,
			"num_predict": 200,
		},
	}
	var resp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := postJSON(ctx, c.hc, c.cfg.Endpoint+"/api/chat", nil, body, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("ollama: %s", resp.Error)
	}
	return resp.Message.Content, nil
}

func (c *ollamaClient) Ping(ctx context.Context) error {
	return pingGET(ctx, c.hc, c.cfg.Endpoint+"/api/tags", nil)
}

func (c *ollamaClient) ModelAvailable(ctx context.Context) (bool, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint+"/api/tags", nil)
	if err != nil {
		return false, false
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return false, false
	}
	for _, m := range tags.Models {
		if m.Name == c.cfg.Model || m.Name == c.cfg.Model+":latest" {
			return true, true
		}
	}
	return false, true
}

// --- OpenAI-compatible (OpenAI, Azure, DeepSeek, custom) ---

type openaiClient struct {
	prov *provider.Provider
	hc   *http.Client
}

func (c *openaiClient) Chat(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": c.prov.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"response_format": map[string]string{"type": "json_object"},
	}
	// OpenAI's current models renamed the output cap and fix temperature at
	// the default; the compatible servers (DeepSeek, LM Studio, llama.cpp,
	// Azure classic API) still speak the original dialect.
	if c.prov.Name == "openai" {
		body["max_completion_tokens"] = 2000 // includes hidden reasoning tokens on gpt-5 models
	} else {
		body["max_tokens"] = 300
		body["temperature"] = 0
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := postJSON(ctx, c.hc, c.prov.ChatURL(), c.prov.Headers(), body, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%s: %s", c.prov.Name, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%s returned no choices", c.prov.Name)
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *openaiClient) Ping(ctx context.Context) error {
	url, ok := c.prov.PingURL()
	if !ok {
		return nil // no cheap probe (azure); doctor does a live call anyway
	}
	return pingGET(ctx, c.hc, url, c.prov.Headers())
}

func (c *openaiClient) ModelAvailable(ctx context.Context) (bool, bool) {
	return false, false // servers list models inconsistently; doctor just tries a call
}

// --- Anthropic Messages API ---

type anthropicClient struct {
	prov *provider.Provider
	hc   *http.Client
}

func (c *anthropicClient) Chat(ctx context.Context, system, user string) (string, error) {
	// No native JSON mode and no prefill (assistant prefill is rejected by
	// current Anthropic models): the system prompt demands bare JSON and the
	// local validator in suggest.Parse stays the source of truth.
	body := map[string]any{
		"model":       c.prov.Model,
		"max_tokens":  300,
		"temperature": 0,
		"system":      system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	var buf bytes.Buffer
	if err := postRaw(ctx, c.hc, c.prov.ChatURL(), c.prov.Headers(), body, &buf); err != nil {
		return "", err
	}
	return parseAnthropic(buf.Bytes())
}

// parseAnthropic extracts the text answer (or the API error) from a
// Messages API response body.
func parseAnthropic(data []byte) (string, error) {
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("anthropic: invalid response: %v", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("anthropic: %s: %s", resp.Error.Type, resp.Error.Message)
	}
	for _, b := range resp.Content {
		if b.Type == "text" && b.Text != "" {
			return b.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic returned no text content")
}

func (c *anthropicClient) Ping(ctx context.Context) error {
	url, _ := c.prov.PingURL()
	return pingGET(ctx, c.hc, url, c.prov.Headers())
}

func (c *anthropicClient) ModelAvailable(ctx context.Context) (bool, bool) {
	return false, false
}

// --- shared HTTP plumbing ---

func pingGET(ctx context.Context, hc *http.Client, url string, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("authentication failed (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		return fmt.Errorf("model server returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func postJSON(ctx context.Context, hc *http.Client, url string, headers map[string]string, body, out any) error {
	var buf bytes.Buffer
	if err := postRaw(ctx, hc, url, headers, body, &buf); err != nil {
		return err
	}
	return json.Unmarshal(buf.Bytes(), out)
}

// postRaw POSTs body as JSON and writes the raw response into out. Non-200
// responses become errors that keep the HTTP status visible so callers can
// tell auth failures from missing deployments.
func postRaw(ctx context.Context, hc *http.Client, url string, headers map[string]string, body any, out *bytes.Buffer) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	logHTTP(url, b)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(data)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("model server HTTP %d: %s", resp.StatusCode, msg)
	}
	out.Write(data)
	return nil
}

var httpLogMu sync.Mutex

// logHTTP appends the outgoing URL and request body to the file named by
// NUDGE_HTTP_LOG. Debug aid: it proves what leaves the machine (masked
// input) and what doesn't (tier-1 answers never log anything). Auth headers
// are never written.
func logHTTP(url string, body []byte) {
	path := os.Getenv("NUDGE_HTTP_LOG")
	if path == "" {
		return
	}
	httpLogMu.Lock()
	defer httpLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s POST %s %s\n", time.Now().Format(time.RFC3339), url, body)
}
