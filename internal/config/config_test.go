package config

import (
	"strings"
	"testing"
)

func TestRequireLoopback(t *testing.T) {
	for _, ep := range []string{
		"http://localhost:11434",
		"http://127.0.0.1:8080",
		"http://[::1]:11434",
	} {
		if err := requireLoopback(ep); err != nil {
			t.Errorf("requireLoopback(%q) = %v, want nil", ep, err)
		}
	}
	for _, ep := range []string{
		"http://192.168.1.10:11434",
		"https://api.openai.com",
		"http://myserver.local:11434",
	} {
		if err := requireLoopback(ep); err == nil {
			t.Errorf("requireLoopback(%q) should fail — nothing may leave the machine", ep)
		}
	}
}

func TestParseAndEnvOverride(t *testing.T) {
	c := Defaults()
	for k, v := range map[string]string{
		"model":           "qwen2.5-coder:3b",
		"endpoint":        "http://localhost:9999/",
		"timeout":         "5",
		"confidence":      "0.75",
		"allow_non_local": "true",
		"keep_alive":      "30m",
		"backend":         "OPENAI",
	} {
		set(&c, k, v)
	}
	if c.Model != "qwen2.5-coder:3b" || c.TimeoutSec != 5 || c.Confidence != 0.75 ||
		!c.AllowNonLocal || c.KeepAlive != "30m" || c.Backend != "openai" {
		t.Errorf("set results wrong: %+v", c)
	}
	if strings.HasSuffix(c.Endpoint, "/") {
		t.Errorf("endpoint should be normalized: %q", c.Endpoint)
	}

	// bad values are ignored, defaults kept
	d := Defaults()
	set(&d, "timeout", "banana")
	set(&d, "confidence", "7")
	if d.TimeoutSec != Defaults().TimeoutSec || d.Confidence != Defaults().Confidence {
		t.Errorf("invalid values must be ignored: %+v", d)
	}

	t.Setenv("NUDGE_MODEL", "llama3.2:3b")
	e := Defaults()
	applyEnv(&e)
	if e.Model != "llama3.2:3b" {
		t.Errorf("env override failed: %+v", e)
	}
}
