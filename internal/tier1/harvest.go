package tier1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Everything in this file is derived from the machine at runtime: executable
// names come from PATH, subcommand names come from the tools' own help
// output. Results are cached in the user cache dir; a cache is invalidated
// when the thing it was derived from changes (PATH value, tool binary).

const pathCacheTTL = 24 * time.Hour

// CacheDir returns the nudge cache directory, creating it if needed.
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "nudge")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

type pathCache struct {
	PathHash string    `json:"path_hash"`
	When     time.Time `json:"when"`
	Names    []string  `json:"names"`
}

// Executables returns the deduplicated basenames of every executable on PATH
// (Windows: PATHEXT extensions stripped). Cached keyed on a hash of PATH.
func Executables() []string {
	pathVal := os.Getenv("PATH")
	h := sha256.Sum256([]byte(pathVal))
	key := hex.EncodeToString(h[:8])
	cacheFile := filepath.Join(CacheDir(), "path-exes.json")

	var pc pathCache
	if b, err := os.ReadFile(cacheFile); err == nil {
		if json.Unmarshal(b, &pc) == nil && pc.PathHash == key && time.Since(pc.When) < pathCacheTTL {
			return pc.Names
		}
	}

	names := scanPath(pathVal)
	pc = pathCache{PathHash: key, When: time.Now(), Names: names}
	if b, err := json.Marshal(pc); err == nil {
		_ = os.WriteFile(cacheFile, b, 0o644)
	}
	return names
}

func scanPath(pathVal string) []string {
	exts := map[string]bool{}
	windows := runtime.GOOS == "windows"
	if windows {
		pathext := os.Getenv("PATHEXT")
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD;.PS1"
		}
		for _, e := range strings.Split(pathext, ";") {
			if e != "" {
				exts[strings.ToLower(e)] = true
			}
		}
	}

	set := map[string]bool{}
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if windows {
				ext := strings.ToLower(filepath.Ext(name))
				if !exts[ext] {
					continue
				}
				name = strings.TrimSuffix(name, filepath.Ext(name))
			} else {
				if info, err := e.Info(); err != nil || info.Mode()&0o111 == 0 {
					continue
				}
			}
			name = strings.ToLower(name)
			if len(name) >= 2 {
				set[name] = true
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// multiCmdTools is the known set of multi-command CLIs whose subcommands we
// harvest from their own help output. This is a list of tools to *ask*, not
// a list of answers — the subcommand names themselves always come from the
// machine.
var multiCmdTools = map[string][]string{
	"git":     {"help", "-a"},
	"dotnet":  {"--help"},
	"docker":  {"--help"},
	"podman":  {"--help"},
	"kubectl": {"--help"},
	"npm":     {"help"},
	"pnpm":    {"--help"},
	"yarn":    {"--help"},
	"cargo":   {"--list"},
	"go":      {"help"},
	"pip":     {"help"},
	"mvn":     {"--help"},
	"gradle":  {"tasks", "--quiet"},
	"helm":    {"--help"},
	"terraform": {"--help"},
	"az":      {"--help"},
	"gh":      {"--help"},
}

// IsMultiCmdTool reports whether we know how to harvest subcommands for tool.
func IsMultiCmdTool(tool string) bool {
	_, ok := multiCmdTools[strings.ToLower(tool)]
	return ok
}

type subCache struct {
	ExePath string   `json:"exe_path"`
	BinKey  string   `json:"bin_key"`
	Subs    []string `json:"subs"`
}

// Subcommands returns the harvested subcommand list for a known tool, or nil
// if the tool is unknown, missing, or its help output couldn't be parsed
// (in which case Tier 1 silently skips it). The resolved exe path is stored
// in the cache so the warm path costs one Stat, not a PATH walk.
func Subcommands(tool string) []string {
	tool = strings.ToLower(tool)
	args, ok := multiCmdTools[tool]
	if !ok {
		return nil
	}
	cacheFile := filepath.Join(CacheDir(), "subs-"+tool+".json")
	var sc subCache
	if b, err := os.ReadFile(cacheFile); err == nil {
		if json.Unmarshal(b, &sc) == nil && sc.ExePath != "" && sc.BinKey == binKey(sc.ExePath) {
			return sc.Subs
		}
	}

	exe, err := exec.LookPath(tool)
	if err != nil {
		return nil
	}
	subs := harvestHelp(exe, args)
	sc = subCache{ExePath: exe, BinKey: binKey(exe), Subs: subs}
	if b, err := json.Marshal(sc); err == nil {
		_ = os.WriteFile(cacheFile, b, 0o644)
	}
	return subs
}

// binKey identifies a tool version cheaply: path + size + mtime of the
// binary. Avoids spawning `tool --version` on every cache check.
func binKey(exe string) string {
	info, err := os.Stat(exe)
	if err != nil {
		return exe
	}
	return fmt.Sprintf("%s|%d|%d", exe, info.Size(), info.ModTime().Unix())
}

var subTokenRe = regexp.MustCompile(`^[a-z][a-z0-9][a-z0-9-]*$`)

// harvestHelp runs the tool's help command and heuristically extracts
// subcommand names: indented lines whose first token looks like a command
// word. Lines that are entirely command-like tokens (git's columned
// `help -a` output) contribute every token.
func harvestHelp(exe string, args []string) []string {
	cmd := exec.Command(exe, args...)
	out, _ := cmd.CombinedOutput() // many tools print help to stderr or exit nonzero
	if len(out) == 0 {
		return nil
	}
	return parseHelpText(string(out))
}

func parseHelpText(out string) []string {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		rest := strings.TrimLeft(line, " \t")
		indent := len(line) - len(rest)
		if indent < 2 || rest == "" || strings.HasPrefix(rest, "-") {
			continue
		}
		fields := strings.Fields(rest)
		allCmdLike := true
		for _, f := range fields {
			if !subTokenRe.MatchString(f) {
				allCmdLike = false
				break
			}
		}
		if allCmdLike && len(fields) > 1 {
			for _, f := range fields {
				set[f] = true
			}
			continue
		}
		// "  build     Build a project" style: token, gap, description.
		if subTokenRe.MatchString(fields[0]) && (len(fields) == 1 || strings.Contains(rest[len(fields[0]):], "  ")) {
			set[fields[0]] = true
		}
	}
	if len(set) < 3 {
		return nil // parse failure — silently skip Tier 1 for this tool
	}
	subs := make([]string, 0, len(set))
	for s := range set {
		subs = append(subs, s)
	}
	sort.Strings(subs)
	return subs
}
