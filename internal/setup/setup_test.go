package setup

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCommand(t *testing.T) {
	tests := []struct {
		goos    string
		brew    bool
		wantCmd string
	}{
		{"windows", false, "winget install -e --id Ollama.Ollama"},
		{"darwin", true, "brew install ollama"},
		{"darwin", false, ""},
		{"linux", false, "curl -fsSL https://ollama.com/install.sh | sh"},
	}
	for _, tt := range tests {
		cmd, note := installCommand(tt.goos, tt.brew)
		if cmd != tt.wantCmd {
			t.Errorf("installCommand(%q, %v) = %q, want %q", tt.goos, tt.brew, cmd, tt.wantCmd)
		}
		if cmd == "" && note == "" {
			t.Errorf("installCommand(%q, %v): no command must come with a note", tt.goos, tt.brew)
		}
	}
}

func TestOllamaCandidates(t *testing.T) {
	lad := filepath.Join("C:", "Users", "u", "AppData", "Local")
	got := ollamaCandidates("windows", lad)
	want := filepath.Join(lad, "Programs", "Ollama", "ollama.exe")
	if len(got) != 1 || got[0] != want {
		t.Errorf("windows candidates = %v, want [%s]", got, want)
	}
	if got := ollamaCandidates("windows", ""); got != nil {
		t.Errorf("windows without LOCALAPPDATA must have no candidates, got %v", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		cands := ollamaCandidates(goos, "")
		if len(cands) == 0 {
			t.Errorf("%s must have candidates", goos)
		}
		for _, c := range cands {
			if !strings.HasSuffix(c, "/ollama") {
				t.Errorf("%s candidate %q does not end in /ollama", goos, c)
			}
		}
	}
}

func TestSetConfigKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("provider = \"anthropic\"\nmodel = \"claude-haiku-4-5\"\napi_key = \"old\"\napi_key_env = \"MY_VAR\"\n"), 0o644)
	if err := setConfigKey(p, "new-key"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{`provider = "anthropic"`, `model = "claude-haiku-4-5"`, `api_key_env = "MY_VAR"`, `api_key = "new-key"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"old"`) {
		t.Errorf("old key survived:\n%s", s)
	}
	if strings.Count(s, "api_key =") != 1 {
		t.Errorf("api_key line not unique:\n%s", s)
	}
}

func TestPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !portBusy("http://" + ln.Addr().String()) {
		t.Error("open listener must count as busy")
	}
	if portBusy("http://127.0.0.1:1") {
		t.Error("closed port must not count as busy")
	}
	if portBusy(":::not-a-url") {
		t.Error("garbage endpoint must not count as busy")
	}
}

func TestTailLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	if got := tailLines(p, 5); got != nil {
		t.Errorf("missing file: got %v", got)
	}
	os.WriteFile(p, []byte("one\ntwo\r\nthree\nfour\n"), 0o644)
	got := tailLines(p, 2)
	if len(got) != 2 || got[0] != "three" || got[1] != "four" {
		t.Errorf("tail 2: got %v", got)
	}
	if got := tailLines(p, 10); len(got) != 4 || got[1] != "two" {
		t.Errorf("tail beyond length must return all lines, CRLF stripped: %v", got)
	}
}

func TestPolicyBlocksProfiles(t *testing.T) {
	for _, pol := range []string{"Restricted", "AllSigned", "Undefined"} {
		if !policyBlocksProfiles(pol) {
			t.Errorf("%s must count as blocking", pol)
		}
	}
	for _, pol := range []string{"RemoteSigned", "Unrestricted", "Bypass"} {
		if policyBlocksProfiles(pol) {
			t.Errorf("%s must not count as blocking", pol)
		}
	}
}

func TestHasModel(t *testing.T) {
	models := []string{"llama3:8b", "qwen2.5-coder:1.5b", "mistral:latest"}
	if !hasModel(models, "qwen2.5-coder:1.5b") {
		t.Error("exact match not found")
	}
	if !hasModel(models, "mistral") {
		t.Error(":latest fallback not applied")
	}
	if hasModel(models, "qwen2.5-coder:3b") {
		t.Error("missing model reported as present")
	}
}

func TestFmtBytes(t *testing.T) {
	if got := fmtBytes(512 << 20); got != "512 MB" {
		t.Errorf("512 MB: got %q", got)
	}
	if got := fmtBytes(1610612736); got != "1.5 GB" {
		t.Errorf("1.5 GB: got %q", got)
	}
}

func TestServerUpAndListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"models":[{"name":"qwen2.5-coder:1.5b"},{"name":"llama3:8b"}]}`)
	}))
	defer srv.Close()

	if !serverUp(srv.URL) {
		t.Fatal("serverUp = false for a live server")
	}
	models, err := listModels(srv.URL)
	if err != nil {
		t.Fatalf("listModels: %v", err)
	}
	if len(models) != 2 || models[0] != "qwen2.5-coder:1.5b" {
		t.Errorf("listModels = %v", models)
	}
	if serverUp("http://127.0.0.1:1") {
		t.Error("serverUp = true for a dead endpoint")
	}
}

func TestPullModelSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"status":"downloading abc","completed":100,"total":1000}`)
		fmt.Fprintln(w, `{"status":"downloading abc","completed":1000,"total":1000}`)
		fmt.Fprintln(w, `{"status":"success"}`)
	}))
	defer srv.Close()

	var events []string
	err := pullModel(context.Background(), srv.URL, "m", func(status string, completed, total int64) {
		events = append(events, fmt.Sprintf("%s %d/%d", status, completed, total))
	})
	if err != nil {
		t.Fatalf("pullModel: %v", err)
	}
	if len(events) != 4 || events[3] != "success 0/0" {
		t.Errorf("progress events = %v", events)
	}
}

func TestPullModelServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"error":"pull model manifest: file does not exist"}`)
	}))
	defer srv.Close()

	err := pullModel(context.Background(), srv.URL, "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected the ollama error, got %v", err)
	}
}

func TestPullModelTruncatedStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"downloading","completed":1,"total":10}`)
	}))
	defer srv.Close()

	err := pullModel(context.Background(), srv.URL, "m", nil)
	if err == nil || !strings.Contains(err.Error(), "without success") {
		t.Errorf("expected truncated-stream error, got %v", err)
	}
}

func TestPullModelHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := pullModel(context.Background(), srv.URL, "m", nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected HTTP 500 error, got %v", err)
	}
}

func TestAppendLineCreatesAndPreserves(t *testing.T) {
	dir := t.TempDir()

	// Creates the file and parent directories.
	rc := filepath.Join(dir, "sub", "config.fish")
	if err := appendLine(rc, "nudge init fish | source"); err != nil {
		t.Fatalf("appendLine (new file): %v", err)
	}
	got, _ := os.ReadFile(rc)
	if string(got) != "nudge init fish | source\n" {
		t.Errorf("new file content = %q", got)
	}

	// Appends to existing content without touching it, fixing a missing
	// trailing newline first.
	rc2 := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(rc2, []byte("export FOO=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendLine(rc2, `eval "$(nudge init bash)"`); err != nil {
		t.Fatalf("appendLine (existing): %v", err)
	}
	got, _ = os.ReadFile(rc2)
	want := "export FOO=1\neval \"$(nudge init bash)\"\n"
	if string(got) != want {
		t.Errorf("appended content = %q, want %q", got, want)
	}
}

func TestFileContains(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".zshrc")

	if ok, err := fileContains(rc, integrationMarker); err != nil || ok {
		t.Errorf("missing file: got (%v, %v), want (false, nil)", ok, err)
	}
	if err := os.WriteFile(rc, []byte(`eval "$(nudge init zsh)"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fileContains(rc, integrationMarker); !ok {
		t.Error("marker not detected in rc file")
	}
}
