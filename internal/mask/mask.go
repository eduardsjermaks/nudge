// Package mask hides likely secrets before input is sent to a cloud
// provider, and restores them verbatim in whatever the model returns. Each
// distinct secret gets a stable placeholder («SECRET_1», «SECRET_2», …), so
// a command echoed back with the placeholder still runs correctly after
// Restore. Local providers skip this entirely.
//
// Detection is heuristic and errs toward masking: a masked non-secret (say,
// a commit SHA) is restored verbatim and costs only a little model context,
// while a missed secret leaves the machine.
package mask

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// prefixRes match well-known credential formats anywhere in the input.
var prefixRes = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),                                      // OpenAI / Anthropic (sk-ant-…) / many others
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),                                 // GitHub tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),                               // GitHub fine-grained PAT
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{15,}`),                                   // GitLab PAT
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),                               // Slack tokens
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                                         // AWS access key ID
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),                                     // Google API key
	regexp.MustCompile(`\bya29\.[0-9A-Za-z_-]{20,}`),                                   // Google OAuth token
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+`),  // JWT
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`),                                       // npm token
	regexp.MustCompile(`\bdop_v1_[a-f0-9]{30,}`),                                       // DigitalOcean PAT
}

// bearerRe masks the token after "Bearer" (Authorization headers pasted into
// curl lines and the like), keeping the scheme word visible.
var bearerRe = regexp.MustCompile(`(?i)\b(bearer\s+)([A-Za-z0-9._~+/=-]{8,})`)

// passFlagRe masks the value of password-looking flags:
// --password=x, --password x, --token x, --api-key x, --secret x, -p x.
var passFlagRe = regexp.MustCompile(`(?i)(--?(?:password|passwd|pass|token|api[-_]?key|apikey|secret|client[-_]?secret)(?:=|\s+))(\S+)`)

// bareP matches `-p <value>`; the value is masked only when it doesn't look
// like a port mapping or plain number (docker -p 8080:80 stays intact).
var barePRe = regexp.MustCompile(`(^|\s)(-p\s+)(\S+)`)

// attachedPRe matches mysql-style `-pSECRET` (value glued to the flag).
var attachedPRe = regexp.MustCompile(`(^|\s)-p([A-Za-z][A-Za-z0-9!@#$%^&*_+=.-]{7,})`)

var portLikeRe = regexp.MustCompile(`^[0-9:.\[\]]+$`)

// entropyTokenRe pre-filters candidates for the high-entropy check.
var entropyTokenRe = regexp.MustCompile(`[A-Za-z0-9_+/=-]{20,}`)

// Mask replaces likely secrets in s with stable placeholders. It returns the
// masked text and the placeholder→original map for Restore. The map is empty
// (nil-safe to use) when nothing was masked.
func Mask(s string) (string, map[string]string) {
	secrets := map[string]string{} // placeholder -> original
	index := map[string]string{}   // original -> placeholder (stable)
	n := 0
	put := func(orig string) string {
		if p, ok := index[orig]; ok {
			return p
		}
		n++
		p := fmt.Sprintf("«SECRET_%d»", n)
		index[orig] = p
		secrets[p] = orig
		return p
	}

	s = bearerRe.ReplaceAllStringFunc(s, func(m string) string {
		g := bearerRe.FindStringSubmatch(m)
		return g[1] + put(g[2])
	})
	s = passFlagRe.ReplaceAllStringFunc(s, func(m string) string {
		g := passFlagRe.FindStringSubmatch(m)
		return g[1] + put(g[2])
	})
	s = barePRe.ReplaceAllStringFunc(s, func(m string) string {
		g := barePRe.FindStringSubmatch(m)
		if portLikeRe.MatchString(g[3]) || strings.Contains(g[3], "«") {
			return m
		}
		return g[1] + g[2] + put(g[3])
	})
	s = attachedPRe.ReplaceAllStringFunc(s, func(m string) string {
		g := attachedPRe.FindStringSubmatch(m)
		return g[1] + "-p" + put(g[2])
	})
	for _, re := range prefixRes {
		s = re.ReplaceAllStringFunc(s, put)
	}
	s = entropyTokenRe.ReplaceAllStringFunc(s, func(tok string) string {
		if strings.Contains(tok, "«") || !highEntropy(tok) {
			return tok
		}
		return put(tok)
	})
	return s, secrets
}

// Restore puts the original values back wherever a placeholder appears.
func Restore(s string, secrets map[string]string) string {
	for placeholder, orig := range secrets {
		s = strings.ReplaceAll(s, placeholder, orig)
	}
	return s
}

// highEntropy reports whether tok looks like random credential material:
// long, mixed letters and digits, and high Shannon entropy. Plain words,
// paths, and version strings fall below the bar.
func highEntropy(tok string) bool {
	hasLetter, hasDigit := false, false
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	if !hasLetter || !hasDigit {
		return false
	}
	return shannon(tok) >= 3.5
}

func shannon(s string) float64 {
	freq := map[rune]int{}
	for _, r := range s {
		freq[r]++
	}
	total := float64(len([]rune(s)))
	e := 0.0
	for _, c := range freq {
		p := float64(c) / total
		e -= p * math.Log2(p)
	}
	return e
}
