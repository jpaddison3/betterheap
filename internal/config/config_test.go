package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAPITokenPrecedence(t *testing.T) {
	t.Setenv("BETTERHEAP_API_TOKEN", "")
	t.Setenv("BETTERSTACK_API_TOKEN", "")
	s := &Store{File: File{APIToken: "file-tok"}}

	if got := s.APIToken(""); got != "file-tok" {
		t.Errorf("file fallback = %q; want file-tok", got)
	}
	t.Setenv("BETTERSTACK_API_TOKEN", "bs-tok")
	if got := s.APIToken(""); got != "bs-tok" {
		t.Errorf("betterstack env = %q; want bs-tok", got)
	}
	t.Setenv("BETTERHEAP_API_TOKEN", "bh-tok")
	if got := s.APIToken(""); got != "bh-tok" {
		t.Errorf("betterheap env = %q; want bh-tok", got)
	}
	if got := s.APIToken("flag-tok"); got != "flag-tok" {
		t.Errorf("flag = %q; want flag-tok", got)
	}
}

func TestQueryCredsPrecedence(t *testing.T) {
	t.Setenv("BETTERHEAP_QUERY_USERNAME", "")
	t.Setenv("BETTERSTACK_QUERY_USERNAME", "bs-user")
	s := &Store{File: File{QueryUsername: "file-user"}}
	if got := s.QueryUsername(""); got != "bs-user" {
		t.Errorf("query user = %q; want bs-user (betterstack beats file)", got)
	}
}

func TestLimitFallback(t *testing.T) {
	if got := (&Store{}).Limit(0); got != DefaultLimit {
		t.Errorf("default limit = %d; want %d", got, DefaultLimit)
	}
	if got := (&Store{File: File{Limit: 25}}).Limit(0); got != 25 {
		t.Errorf("file limit = %d; want 25", got)
	}
	if got := (&Store{File: File{Limit: 25}}).Limit(7); got != 7 {
		t.Errorf("flag limit = %d; want 7", got)
	}
	bs := &Store{}
	bs.bslog.DefaultLimit = 40
	if got := bs.Limit(0); got != 40 {
		t.Errorf("bslog limit = %d; want 40", got)
	}
}

func TestRegionDefault(t *testing.T) {
	t.Setenv("BETTERHEAP_REGION", "")
	t.Setenv("BETTERSTACK_REGION", "")
	if got := (&Store{}).Region(""); got != DefaultRegion {
		t.Errorf("region default = %q; want %q", got, DefaultRegion)
	}
	if got := (&Store{}).Region("us-chi-1"); got != "us-chi-1" {
		t.Errorf("region flag = %q; want us-chi-1", got)
	}
}

func TestBslogFallbacks(t *testing.T) {
	s := &Store{}
	s.bslog.DefaultSource = "myapp"
	s.bslog.QueryBaseURL = "https://custom-host"
	if got := s.Source(""); got != "myapp" {
		t.Errorf("source bslog fallback = %q; want myapp", got)
	}
	if got := s.QueryHost(""); got != "https://custom-host" {
		t.Errorf("query host bslog fallback = %q; want https://custom-host", got)
	}
}

func TestParseEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	content := "# Better Stack creds\nexport BETTERSTACK_API_TOKEN=tok123\n" +
		"export BETTERSTACK_QUERY_USERNAME=\"user1\"\n" +
		"BETTERSTACK_QUERY_PASSWORD='pw1'\nexport PATH=/x:/y\n\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m := parseEnvFile(p)
	want := map[string]string{
		"BETTERSTACK_API_TOKEN":      "tok123",
		"BETTERSTACK_QUERY_USERNAME": "user1", // double quotes stripped
		"BETTERSTACK_QUERY_PASSWORD": "pw1",   // single quotes stripped
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("parseEnvFile[%s] = %q; want %q", k, m[k], v)
		}
	}
}

func TestParseEnvFileMissingIsEmpty(t *testing.T) {
	if m := parseEnvFile(filepath.Join(t.TempDir(), "nope")); len(m) != 0 {
		t.Errorf("missing file = %v; want empty", m)
	}
}

func TestBslogEnvCredFallback(t *testing.T) {
	t.Setenv("BETTERHEAP_API_TOKEN", "")
	t.Setenv("BETTERSTACK_API_TOKEN", "")
	s := &Store{bslogEnv: map[string]string{"BETTERSTACK_API_TOKEN": "envtok"}}
	if got := s.APIToken(""); got != "envtok" {
		t.Errorf("bslog env fallback = %q; want envtok", got)
	}
	t.Setenv("BETTERSTACK_API_TOKEN", "realenv")
	if got := s.APIToken(""); got != "realenv" {
		t.Errorf("real env should beat bslog env file: got %q", got)
	}
}

func TestSetUnknownKey(t *testing.T) {
	if err := (&Store{}).Set("nope", "x"); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestRedactedMasksSecrets(t *testing.T) {
	s := &Store{File: File{APIToken: "secret", QueryPassword: "pw", DefaultSource: "myapp"}}
	r := s.Redacted()
	if r.APIToken == "secret" || r.APIToken == "" {
		t.Errorf("api token not masked: %q", r.APIToken)
	}
	if r.QueryPassword == "pw" {
		t.Errorf("password not masked: %q", r.QueryPassword)
	}
	if r.DefaultSource != "myapp" {
		t.Errorf("non-secret altered: %q", r.DefaultSource)
	}
}
