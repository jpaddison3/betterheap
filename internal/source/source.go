// Package source resolves a friendly source name into the concrete facts a
// query needs: the live and archive ClickHouse table ids, the region, and the
// envelope profile. Results are cached on disk to avoid a Telemetry API
// round-trip on every command.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/envelope"
)

// Source is a fully resolved log source.
type Source struct {
	Name            string `json:"name"`    // canonical (table_name)
	Display         string `json:"display"` // human display name
	TeamID          int    `json:"team_id"`
	TableName       string `json:"table_name"`
	Platform        string `json:"platform"`
	Profile         string `json:"profile"` // auto-detected envelope profile
	Region          string `json:"region"`
	LogsRetention   int    `json:"logs_retention"` // days
	IngestingPaused bool   `json:"ingesting_paused"`
}

// LiveTable is the hot-buffer table function argument, e.g. t<team>_<source>_logs.
func (s *Source) LiveTable() string {
	return fmt.Sprintf("t%d_%s_logs", s.TeamID, s.TableName)
}

// ArchiveTable is the S3 archive table function argument, e.g. t<team>_<source>_s3.
func (s *Source) ArchiveTable() string {
	return fmt.Sprintf("t%d_%s_s3", s.TeamID, s.TableName)
}

// regionRe matches a canonical Better Stack region like eu-nbg-2 / us-chi-1,
// stopping at the trailing number so ingest suffixes (e.g. "-vec" on Vector
// hosts) are dropped.
var regionRe = regexp.MustCompile(`[a-z]{2}-[a-z]+-\d+`)

// RegionFromHost extracts the query region from an ingesting host:
//   - "s12345.eu-nbg-2.betterstackdata.com" -> "eu-nbg-2"
//   - "s67890.eu-fsn-3-vec.betterstackdata.com" -> "eu-fsn-3" (drops -vec)
//
// Returns "" if the host isn't a betterstackdata.com host or has no region.
func RegionFromHost(host string) string {
	const suffix = ".betterstackdata.com"
	rest := strings.TrimSuffix(host, suffix)
	if rest == host { // suffix not present
		return ""
	}
	return regionRe.FindString(rest)
}

// fromClient converts a Telemetry source into a resolved Source, applying region
// derivation and profile auto-detection. defaultRegion is used when the host
// gives no region.
func fromClient(cs client.Source, defaultRegion string) Source {
	region := RegionFromHost(cs.IngestingHost)
	if region == "" {
		region = defaultRegion
	}
	name := cs.TableName
	if name == "" {
		name = cs.Name
	}
	return Source{
		Name:            name,
		Display:         cs.Name,
		TeamID:          cs.TeamID,
		TableName:       cs.TableName,
		Platform:        cs.Platform,
		Profile:         envelope.DetectFromPlatform(cs.Platform),
		Region:          region,
		LogsRetention:   cs.LogsRetention,
		IngestingPaused: cs.IngestingPaused,
	}
}

// Registry lists and resolves sources, caching the Telemetry response on disk.
type Registry struct {
	tel           *client.TelemetryClient
	defaultRegion string
	cachePath     string // "" disables the disk cache
	ttl           time.Duration
}

// NewRegistry builds a registry. cacheDir "" disables caching (e.g. in tests).
func NewRegistry(tel *client.TelemetryClient, defaultRegion, cacheDir string) *Registry {
	r := &Registry{tel: tel, defaultRegion: defaultRegion, ttl: time.Hour}
	if cacheDir != "" {
		r.cachePath = filepath.Join(cacheDir, "sources.json")
	}
	return r
}

type cacheFile struct {
	FetchedAt time.Time `json:"fetched_at"`
	Region    string    `json:"region"`
	Sources   []Source  `json:"sources"`
}

// List returns all sources, served from a fresh disk cache when available.
func (r *Registry) List(ctx context.Context) ([]Source, error) {
	if cached, ok := r.readCache(); ok {
		return cached, nil
	}
	return r.Refresh(ctx)
}

// Refresh always fetches from the Telemetry API and rewrites the cache.
func (r *Registry) Refresh(ctx context.Context) ([]Source, error) {
	if r.tel == nil || r.tel.Token == "" {
		return nil, fmt.Errorf("no Telemetry API token: run `betterheap auth login`, set BETTERHEAP_API_TOKEN, or pass --token")
	}
	cs, err := r.tel.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(cs))
	for _, c := range cs {
		out = append(out, fromClient(c, r.defaultRegion))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	r.writeCache(out)
	return out, nil
}

// Resolve finds a single source by name, matching table_name then display name,
// case-insensitively.
func (r *Registry) Resolve(ctx context.Context, name string) (*Source, error) {
	if name == "" {
		return nil, fmt.Errorf("no source specified: pass a source name, set a default with `betterheap config set source <name>`, or BETTERHEAP_SOURCE")
	}
	list, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if s := match(list, name); s != nil {
		return s, nil
	}
	// A stale cache can miss a newly created source; refresh once before failing.
	if r.cachePath != "" {
		if list, err = r.Refresh(ctx); err == nil {
			if s := match(list, name); s != nil {
				return s, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown source %q (try `betterheap sources list`)", name)
}

func match(list []Source, name string) *Source {
	for i := range list {
		if strings.EqualFold(list[i].TableName, name) || strings.EqualFold(list[i].Name, name) {
			return &list[i]
		}
	}
	for i := range list {
		if strings.EqualFold(list[i].Display, name) {
			return &list[i]
		}
	}
	return nil
}

func (r *Registry) readCache() ([]Source, bool) {
	if r.cachePath == "" {
		return nil, false
	}
	b, err := os.ReadFile(r.cachePath)
	if err != nil {
		return nil, false
	}
	var cf cacheFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, false
	}
	if cf.Region != r.defaultRegion {
		return nil, false // region changed; force refresh
	}
	if time.Since(cf.FetchedAt) > r.ttl {
		return nil, false
	}
	return cf.Sources, true
}

func (r *Registry) writeCache(sources []Source) {
	if r.cachePath == "" {
		return
	}
	cf := cacheFile{FetchedAt: time.Now(), Region: r.defaultRegion, Sources: sources}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.cachePath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(r.cachePath, b, 0o600)
}
