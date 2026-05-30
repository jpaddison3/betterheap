// Package config loads betterheap's config file and resolves credentials and
// defaults across the precedence chain: explicit flag -> BETTERHEAP_* env ->
// BETTERSTACK_* env (bslog compatibility) -> ~/.betterheap/config.json ->
// ~/.bslog/config.json (only when ~/.betterheap is absent).
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultRegion is Better Stack's EU endpoint, used when nothing else resolves.
const DefaultRegion = "eu-nbg-2"

// DefaultLimit caps result rows when neither flag nor config specifies one.
const DefaultLimit = 100

// File is the on-disk shape of ~/.betterheap/config.json. Credentials live here
// after `auth login`; the rest are user defaults.
type File struct {
	APIToken      string `json:"api_token,omitempty"`
	QueryUsername string `json:"query_username,omitempty"`
	QueryPassword string `json:"query_password,omitempty"`
	Region        string `json:"region,omitempty"`
	QueryHost     string `json:"query_host,omitempty"`
	DefaultSource string `json:"default_source,omitempty"`
	Format        string `json:"format,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Since         string `json:"since,omitempty"`
	Profile       string `json:"profile,omitempty"`
}

// bslogFile is the subset of ~/.bslog/config.json betterheap reads for
// migration fallback.
type bslogFile struct {
	DefaultSource string `json:"defaultSource"`
	DefaultLimit  int    `json:"defaultLimit"`
	OutputFormat  string `json:"outputFormat"`
	QueryBaseURL  string `json:"queryBaseUrl"`
}

// Store holds the loaded config plus bslog fallback defaults. Its getters apply
// the full precedence chain.
type Store struct {
	File     File
	path     string
	bslog    bslogFile
	bslogEnv map[string]string // exports parsed from ~/.bslog/env
	// hasNative is true when ~/.betterheap/config.json exists; bslog defaults
	// are only consulted when it does not.
	hasNative bool
}

// Path returns the config file path, honoring $BETTERHEAP_CONFIG then
// ~/.betterheap/config.json.
func Path() (string, error) {
	if p := os.Getenv("BETTERHEAP_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".betterheap", "config.json"), nil
}

// Load reads the config file (an absent file is not an error) and, when
// ~/.betterheap is absent, the bslog config for fallback defaults.
func Load() (*Store, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	s := &Store{path: p}

	// hasNative keys off the config *file*, not the directory: betterheap writes
	// a cache/ dir under ~/.betterheap, which must not disable the bslog
	// fallback for someone who hasn't run `auth login` yet.
	b, err := os.ReadFile(p)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.File); err != nil {
			return nil, err
		}
		s.hasNative = true
	case errors.Is(err, os.ErrNotExist):
		// fine: empty config
	default:
		return nil, err
	}

	if !s.hasNative {
		s.loadBslog()
	}
	return s, nil
}

func (s *Store) loadBslog() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if b, err := os.ReadFile(filepath.Join(home, ".bslog", "config.json")); err == nil {
		_ = json.Unmarshal(b, &s.bslog) // best-effort fallback
	}
	// bslog stores credentials in a sourced ~/.bslog/env file (export KEY=val),
	// not in its JSON config — read it so betterheap works with zero re-setup.
	s.bslogEnv = parseEnvFile(filepath.Join(home, ".bslog", "env"))
}

// parseEnvFile reads a shell env file of `export KEY=value` (or `KEY=value`)
// lines into a map. Best-effort: unreadable files yield an empty map.
func parseEnvFile(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

func (s *Store) bslogEnvVal(key string) string {
	return s.bslogEnv[key]
}

// Save writes the config file with 0600 permissions, creating ~/.betterheap
// (0700) as needed.
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.File, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}

// FilePath is the resolved config path on disk.
func (s *Store) FilePath() string { return s.path }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// APIToken resolves the Telemetry API token. flag wins, then env, then file,
// then the bslog env file.
func (s *Store) APIToken(flag string) string {
	return firstNonEmpty(flag,
		os.Getenv("BETTERHEAP_API_TOKEN"),
		os.Getenv("BETTERSTACK_API_TOKEN"),
		s.File.APIToken,
		s.bslogEnvVal("BETTERSTACK_API_TOKEN"),
	)
}

// QueryUsername resolves the ClickHouse query username.
func (s *Store) QueryUsername(flag string) string {
	return firstNonEmpty(flag,
		os.Getenv("BETTERHEAP_QUERY_USERNAME"),
		os.Getenv("BETTERSTACK_QUERY_USERNAME"),
		s.File.QueryUsername,
		s.bslogEnvVal("BETTERSTACK_QUERY_USERNAME"),
	)
}

// QueryPassword resolves the ClickHouse query password.
func (s *Store) QueryPassword(flag string) string {
	return firstNonEmpty(flag,
		os.Getenv("BETTERHEAP_QUERY_PASSWORD"),
		os.Getenv("BETTERSTACK_QUERY_PASSWORD"),
		s.File.QueryPassword,
		s.bslogEnvVal("BETTERSTACK_QUERY_PASSWORD"),
	)
}

// QueryHost resolves a full query host/URL override, if any. Empty means "build
// the URL from the region." bslog's queryBaseUrl is the last fallback.
func (s *Store) QueryHost(flag string) string {
	return firstNonEmpty(flag,
		os.Getenv("BETTERHEAP_QUERY_HOST"),
		os.Getenv("BSLOG_QUERY_HOST"),
		s.File.QueryHost,
		s.bslogEnvVal("BSLOG_QUERY_HOST"),
		s.bslog.QueryBaseURL,
	)
}

// Region resolves the Better Stack region (e.g. eu-nbg-2). A per-source region
// derived from the Telemetry API takes precedence over this default and is
// applied by the source package, not here.
func (s *Store) Region(flag string) string {
	return firstNonEmpty(flag,
		os.Getenv("BETTERHEAP_REGION"),
		os.Getenv("BETTERSTACK_REGION"),
		s.File.Region,
		DefaultRegion,
	)
}

// Source resolves the default source name.
func (s *Store) Source(flag string) string {
	return firstNonEmpty(flag,
		os.Getenv("BETTERHEAP_SOURCE"),
		s.File.DefaultSource,
		s.bslog.DefaultSource,
	)
}

// Format resolves the output format ("" means TTY-aware auto-selection).
// Note: bslog's outputFormat is intentionally NOT inherited — doing so would
// disable betterheap's TTY-aware default (pretty on a terminal, ndjson piped),
// which is a headline ergonomic. Set it natively with `config set format`.
func (s *Store) Format(flag string) string {
	return firstNonEmpty(flag, s.File.Format)
}

// Profile resolves a forced envelope profile ("" means auto-detect from the
// source platform).
func (s *Store) Profile(flag string) string {
	return firstNonEmpty(flag, s.File.Profile)
}

// Since resolves the default --since value ("" means none).
func (s *Store) Since(flag string) string {
	return firstNonEmpty(flag, s.File.Since)
}

// Limit resolves the row limit. A flag value <= 0 means "unset" so config/env
// can supply it; 0 reaching the caller means DefaultLimit.
func (s *Store) Limit(flag int) int {
	if flag > 0 {
		return flag
	}
	if s.File.Limit > 0 {
		return s.File.Limit
	}
	if s.bslog.DefaultLimit > 0 {
		return s.bslog.DefaultLimit
	}
	return DefaultLimit
}

// Set assigns a settable key in the config file. Unknown keys return an error.
func (s *Store) Set(key, value string) error {
	switch key {
	case "api_token":
		s.File.APIToken = value
	case "query_username":
		s.File.QueryUsername = value
	case "query_password":
		s.File.QueryPassword = value
	case "region":
		s.File.Region = value
	case "query_host":
		s.File.QueryHost = value
	case "default_source", "source":
		s.File.DefaultSource = value
	case "format":
		s.File.Format = value
	case "since":
		s.File.Since = value
	case "profile":
		s.File.Profile = value
	case "limit":
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		s.File.Limit = n
	default:
		return errors.New("unknown config key: " + key)
	}
	return nil
}

// Redacted returns the file config with secrets masked, for display.
func (s *Store) Redacted() File {
	f := s.File
	mask := func(v string) string {
		if v == "" {
			return ""
		}
		return "********"
	}
	f.APIToken = mask(f.APIToken)
	f.QueryPassword = mask(f.QueryPassword)
	return f
}
