// Package llm talks to the local model server. Two wire formats behind one
// interface: Ollama's native API (default — supports keep_alive and JSON
// mode) and the OpenAI-compatible chat completions API (LM Studio,
// llama.cpp server, vLLM, and Ollama itself all speak it).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"nudge/internal/config"
)

// Client is the one interface the rest of nudge sees.
type Client interface {
	// Chat sends a system+user prompt and returns the raw model text.
	Chat(ctx context.Context, system, user string) (string, error)
	// Ping verifies the server is reachable (cheap, no generation).
	Ping(ctx context.Context) error
	// ModelAvailable reports whether the configured model is present, when
	// the backend can tell; ok=false means "can't tell".
	ModelAvailable(ctx context.Context) (available bool, ok bool)
}

func New(cfg config.Config) Client {
	hc := &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second}
	if cfg.Backend == "openai" {
		return &openaiClient{cfg: cfg, hc: hc}
	}
	return &ollamaClient{cfg: cfg, hc: hc}
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
	if err := c.post(ctx, "/api/chat", body, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("ollama: %s", resp.Error)
	}
	return resp.Message.Content, nil
}

func (c *ollamaClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("model server returned HTTP %d", resp.StatusCode)
	}
	return nil
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

func (c *ollamaClient) post(ctx context.Context, path string, body, out any) error {
	return postJSON(ctx, c.hc, c.cfg.Endpoint+path, body, out)
}

// --- OpenAI-compatible ---

type openaiClient struct {
	cfg config.Config
	hc  *http.Client
}

func (c *openaiClient) Chat(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature":     0,
		"max_tokens":      300,
		"response_format": map[string]string{"type": "json_object"},
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
	if err := postJSON(ctx, c.hc, c.cfg.Endpoint+"/v1/chat/completions", body, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("model server: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("model server returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *openaiClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint+"/v1/models", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("model server returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *openaiClient) ModelAvailable(ctx context.Context) (bool, bool) {
	return false, false // generic servers list models inconsistently; doctor just tries a call
}

func postJSON(ctx context.Context, hc *http.Client, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
	return json.Unmarshal(data, out)
}
