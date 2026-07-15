package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudge/internal/config"
	"nudge/internal/provider"
)

func TestOpenAIRequestShape(t *testing.T) {
	var got struct {
		auth, path, contentType string
		body                    map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth = r.Header.Get("Authorization")
		got.path = r.URL.Path
		got.contentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"command\":\"git push\",\"confidence\":0.9}"}}]}`))
	}))
	defer srv.Close()

	prov := &provider.Provider{
		Name: "openai", Protocol: provider.ProtoOpenAI,
		BaseURL: srv.URL, Model: "gpt-5-mini", Cloud: true,
		APIKey: "sk-test-abc", Timeout: 5 * time.Second,
	}
	c := New(config.Defaults(), prov)
	out, err := c.Chat(context.Background(), "sys", "user text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "git push") {
		t.Errorf("unexpected content: %q", out)
	}
	if got.auth != "Bearer sk-test-abc" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if got.path != "/v1/chat/completions" {
		t.Errorf("path = %q", got.path)
	}
	if got.body["model"] != "gpt-5-mini" {
		t.Errorf("model = %v", got.body["model"])
	}
	rf, _ := got.body["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format = %v", got.body["response_format"])
	}
	// openai speaks the current dialect: max_completion_tokens, default temperature
	if _, ok := got.body["max_completion_tokens"]; !ok {
		t.Errorf("openai request missing max_completion_tokens: %v", got.body)
	}
	if _, ok := got.body["max_tokens"]; ok {
		t.Errorf("openai request must not send max_tokens (rejected by gpt-5 models)")
	}
}

func TestCompatibleServersKeepClassicDialect(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer srv.Close()
	prov := &provider.Provider{
		Name: "deepseek", Protocol: provider.ProtoOpenAI,
		BaseURL: srv.URL, Model: "deepseek-chat", Cloud: true,
		APIKey: "k", Timeout: 5 * time.Second,
	}
	c := New(config.Defaults(), prov)
	_, _ = c.Chat(context.Background(), "s", "u")
	if body["max_tokens"] != float64(300) || body["temperature"] != float64(0) {
		t.Errorf("classic dialect lost: %v", body)
	}
}

func TestAnthropicRequestShape(t *testing.T) {
	var got struct {
		key, version, path string
		body               map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.key = r.Header.Get("x-api-key")
		got.version = r.Header.Get("anthropic-version")
		got.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.Write([]byte(`{"content":[{"type":"text","text":"{\"command\":\"git push\",\"confidence\":0.9}"}]}`))
	}))
	defer srv.Close()

	prov := &provider.Provider{
		Name: "anthropic", Protocol: provider.ProtoAnthropic,
		BaseURL: srv.URL, Model: "claude-haiku-4-5", Cloud: true,
		APIKey: "test-ant-key", Timeout: 5 * time.Second,
	}
	c := New(config.Defaults(), prov)
	out, err := c.Chat(context.Background(), "the system prompt", "the user text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "git push") {
		t.Errorf("unexpected content: %q", out)
	}
	if got.key != "test-ant-key" {
		t.Errorf("x-api-key = %q", got.key)
	}
	if got.version != "2023-06-01" {
		t.Errorf("anthropic-version = %q", got.version)
	}
	if got.path != "/v1/messages" {
		t.Errorf("path = %q", got.path)
	}
	if got.body["system"] != "the system prompt" {
		t.Errorf("system = %v (must be top-level, not a message)", got.body["system"])
	}
	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want exactly one user message", got.body["messages"])
	}
	if m := msgs[0].(map[string]any); m["role"] != "user" {
		t.Errorf("message role = %v", m["role"])
	}
	if _, hasMax := got.body["max_tokens"]; !hasMax {
		t.Errorf("max_tokens missing — Anthropic requires it")
	}
}

func TestParseAnthropic(t *testing.T) {
	txt, err := parseAnthropic([]byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`))
	if err != nil || txt != "hello" {
		t.Errorf("got %q, %v", txt, err)
	}
	_, err = parseAnthropic([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	if err == nil || !strings.Contains(err.Error(), "authentication_error") {
		t.Errorf("error not surfaced: %v", err)
	}
	_, err = parseAnthropic([]byte(`{"content":[]}`))
	if err == nil {
		t.Errorf("empty content should error")
	}
	_, err = parseAnthropic([]byte(`not json`))
	if err == nil {
		t.Errorf("garbage should error")
	}
}

func TestPingReportsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	prov := &provider.Provider{
		Name: "openai", Protocol: provider.ProtoOpenAI,
		BaseURL: srv.URL, Model: "gpt-5-mini", Cloud: true,
		APIKey: "bad", Timeout: 5 * time.Second,
	}
	c := New(config.Defaults(), prov)
	err := c.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("Ping = %v, want authentication failure", err)
	}
}
