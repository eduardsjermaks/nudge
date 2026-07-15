package mask

import (
	"strings"
	"testing"
)

func TestMaskAndRestoreRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string // substring that must NOT appear in the masked text
	}{
		{"openai key", "curl -H 'Authorization: Bearer sk-proj-Abc123XyZ456Qwe789Rty012' https://api.example.com", "sk-proj-Abc123XyZ456Qwe789Rty012"},
		{"bearer token", "curl -H \"Authorization: Bearer my.opaque-token_value123\" htps://example.com", "my.opaque-token_value123"},
		{"github pat", "git clone https://ghp_a1B2c3D4e5F6g7H8i9J0k1L2m3N4@github.com/o/r.git", "ghp_a1B2c3D4e5F6g7H8i9J0k1L2m3N4"},
		{"password flag", "mysql --password=Sup3rS3cret! -u root", "Sup3rS3cret!"},
		{"password flag space", "psql --password hunter2secret", "hunter2secret"},
		{"bare -p", "sshpass -p MyS3cretPass ssh host", "MyS3cretPass"},
		{"attached -p", "mysql -pS3cretV4lue -u root db", "S3cretV4lue"},
		{"aws key", "aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"jwt", "curl -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiIs.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N4' x", "eyJhbGciOiJIUzI1NiIs"},
		{"high entropy", "export STRIPE_KEY=rk4J9zQ2mXw7Lp0vB6tY3nHs8dF1cG5a", "rk4J9zQ2mXw7Lp0vB6tY3nHs8dF1cG5a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			masked, secrets := Mask(c.in)
			if strings.Contains(masked, c.secret) {
				t.Fatalf("secret leaked into masked text: %q", masked)
			}
			if len(secrets) == 0 {
				t.Fatalf("nothing was masked in %q", c.in)
			}
			if !strings.Contains(masked, "«SECRET_") {
				t.Fatalf("no placeholder in masked text: %q", masked)
			}
			if got := Restore(masked, secrets); got != c.in {
				t.Errorf("round trip failed:\n in:  %q\n out: %q", c.in, got)
			}
		})
	}
}

func TestMaskLeavesNormalCommandsAlone(t *testing.T) {
	for _, in := range []string{
		"git push origin main",
		"docker run -p 8080:80 nginx",
		"dotnet ef migrations add AddOrders",
		"kubectl get pods --all-namespaces",
		"go test ./... -run TestFoo -timeout 30m",
		"curl https://api.example.com/v1/users?page=2",
	} {
		masked, secrets := Mask(in)
		if masked != in || len(secrets) != 0 {
			t.Errorf("false positive: %q became %q (secrets %v)", in, masked, secrets)
		}
	}
}

func TestStablePlaceholders(t *testing.T) {
	in := "echo sk-proj-Abc123XyZ456Qwe789Rty012 && echo sk-proj-Abc123XyZ456Qwe789Rty012"
	masked, secrets := Mask(in)
	if len(secrets) != 1 {
		t.Fatalf("same secret should get one placeholder, got %d: %v", len(secrets), secrets)
	}
	if strings.Count(masked, "«SECRET_1»") != 2 {
		t.Errorf("placeholder not reused: %q", masked)
	}
}

func TestRestoreInModelOutput(t *testing.T) {
	// The model rewrites the command but keeps the placeholder — restore
	// must put the original token back.
	_, secrets := Mask("curl -H 'Authorization: Bearer tok_A1b2C3d4E5f6' htps://api.example.com")
	out := Restore("curl -H 'Authorization: Bearer «SECRET_1»' https://api.example.com", secrets)
	if !strings.Contains(out, "tok_A1b2C3d4E5f6") {
		t.Errorf("token not restored: %q", out)
	}
}
