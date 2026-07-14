// Package config loads runtime configuration: which local endpoint and model
// to talk to, timeouts, and the confidence threshold. This is infrastructure
// configuration — nudge has no matching configuration of any kind.
//
// File format is a flat TOML subset: `key = value` lines, `#` comments,
// strings quoted or bare, booleans, numbers. Parsed by hand to stay
// dependency-free.
package config

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	Backend       string  // "ollama" (native API) or "openai" (any OpenAI-compatible server)
	Endpoint      string  // base URL, default http://localhost:11434
	Model         string  // default qwen2.5-coder:1.5b
	KeepAlive     string  // Ollama keep_alive, e.g. "10m"
	TimeoutSec    int     // per-request timeout
	Confidence    float64 // below this a suggestion is labeled "best guess"
	AllowNonLocal bool    // opt-out of the loopback-only guard
}

func Defaults() Config {
	return Config{
		Backend:    "ollama",
		Endpoint:   "http://localhost:11434",
		Model:      "qwen2.5-coder:1.5b",
		KeepAlive:  "10m",
		TimeoutSec: 30,
		Confidence: 0.6,
	}
}

// Path returns the config file location:
// Windows %APPDATA%\nudge\config.toml, elsewhere ~/.config/nudge/config.toml.
func Path() string {
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "nudge", "config.toml")
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "nudge", "config.toml")
}

// Load reads the config file (if any), applies env-var overrides, and
// enforces the loopback-only rule.
func Load() (Config, error) {
	c := Defaults()
	if f, err := os.Open(Path()); err == nil {
		defer f.Close()
		parseInto(&c, f)
	}
	applyEnv(&c)
	if !c.AllowNonLocal {
		if err := requireLoopback(c.Endpoint); err != nil {
			return c, err
		}
	}
	return c, nil
}

func parseInto(c *Config, f *os.File) {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		set(c, k, v)
	}
}

func applyEnv(c *Config) {
	for k, key := range map[string]string{
		"NUDGE_BACKEND":         "backend",
		"NUDGE_ENDPOINT":        "endpoint",
		"NUDGE_MODEL":           "model",
		"NUDGE_KEEP_ALIVE":      "keep_alive",
		"NUDGE_TIMEOUT":         "timeout",
		"NUDGE_CONFIDENCE":      "confidence",
		"NUDGE_ALLOW_NON_LOCAL": "allow_non_local",
	} {
		if v := os.Getenv(k); v != "" {
			set(c, key, v)
		}
	}
}

func set(c *Config, key, val string) {
	switch key {
	case "backend":
		c.Backend = strings.ToLower(val)
	case "endpoint":
		c.Endpoint = strings.TrimRight(val, "/")
	case "model":
		c.Model = val
	case "keep_alive":
		c.KeepAlive = val
	case "timeout":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			c.TimeoutSec = n
		}
	case "confidence":
		if f, err := strconv.ParseFloat(val, 64); err == nil && f >= 0 && f <= 1 {
			c.Confidence = f
		}
	case "allow_non_local":
		c.AllowNonLocal = val == "true" || val == "1"
	}
}

// requireLoopback hard-fails unless the endpoint host resolves to the local
// machine. This is the privacy guarantee: nothing leaves the machine unless
// the user explicitly sets allow_non_local = true.
func requireLoopback(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint %q: %v", endpoint, err)
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("endpoint %q is not loopback; nudge only talks to local model servers.\nSet allow_non_local = true in %s if you really run your model on another machine", endpoint, Path())
}
