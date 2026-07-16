package execx

import "testing"

func TestRewriteAndChains(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"git push", "git push"},
		{"mkdir test && git init", "mkdir test; if ($?) { git init }"},
		{"mkdir test && cd test && git init", "mkdir test; if ($?) { cd test; if ($?) { git init } }"},
		// quoted input is left alone — "&&" inside quotes must not split
		{`echo "a && b"`, `echo "a && b"`},
		{`echo 'a && b'`, `echo 'a && b'`},
	}
	for _, tt := range tests {
		if got := RewriteAndChains(tt.in); got != tt.want {
			t.Errorf("RewriteAndChains(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
