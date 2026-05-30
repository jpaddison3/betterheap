// Package envelope maps friendly field names (level, message, module, ...) to
// the ClickHouse SQL that extracts them from Better Stack's JSON `raw` column.
//
// This is betterheap's fix for bslog's empty-`level` footgun: Better Stack
// stores everything in a JSON envelope, so flat column names like `level` do
// not exist. Each platform (Vercel, Render, ...) nests fields differently, so
// extraction is profile-driven.
package envelope

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// realColumns are physical ClickHouse columns, queried directly rather than
// extracted from the JSON envelope.
var realColumns = map[string]bool{
	"dt":        true,
	"raw":       true,
	"_row_type": true,
}

// safeField guards every user-supplied field name before it is interpolated
// into a JSON path, preventing SQL injection through --fields / --where keys.
var safeField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Profile is a named set of friendly-name -> SQL-expression mappings plus a
// fallback for fields not explicitly listed (the app logger's structured
// context, which is open-ended).
type Profile struct {
	Name     string
	fields   map[string]string
	fallback func(field string) string
}

// Vercel is the envelope produced by Vercel -> Better Stack ingestion. Paths
// are ground-truthed against a live Vercel source.
var Vercel = Profile{
	Name: "vercel",
	fields: map[string]string{
		"level":      `COALESCE(NULLIF(JSON_VALUE(raw,'$.vercel.level'),''), NULLIF(JSONExtractString(raw,'level'),''), NULLIF(JSON_VALUE(raw,'$.levelName'),''))`,
		"message":    `JSON_VALUE(raw,'$.message')`,
		"module":     `JSON_VALUE(raw,'$.message_json.module')`,
		"env":        `JSON_VALUE(raw,'$.vercel.environment')`,
		"status":     `toInt32OrNull(COALESCE(NULLIF(JSON_VALUE(raw,'$.vercel.proxy.status_code'),''), JSON_VALUE(raw,'$.vercel.statusCode')))`,
		"request_id": `COALESCE(NULLIF(JSON_VALUE(raw,'$.vercel.request_id'),''), JSON_VALUE(raw,'$.request_id'))`,
		"path":       `JSON_VALUE(raw,'$.vercel.path')`,
	},
	// The app logger's structured context lands under $.message_json.*, so an
	// unknown field is most likely a logged context key.
	fallback: func(field string) string {
		return fmt.Sprintf("JSON_VALUE(raw,'$.message_json.%s')", field)
	},
}

// Render is the envelope produced by Render -> Better Stack ingestion. Render's
// shape differs from Vercel's; these paths are best-effort and overridable in
// config until confirmed against a live Render source.
var Render = Profile{
	Name: "render",
	fields: map[string]string{
		"level":      `COALESCE(NULLIF(JSON_VALUE(raw,'$.level'),''), NULLIF(JSON_VALUE(raw,'$.severity'),''), NULLIF(JSON_VALUE(raw,'$.levelName'),''))`,
		"message":    `COALESCE(NULLIF(JSON_VALUE(raw,'$.message'),''), JSON_VALUE(raw,'$.msg'))`,
		"module":     `JSON_VALUE(raw,'$.module')`,
		"env":        `JSON_VALUE(raw,'$.environment')`,
		"request_id": `JSON_VALUE(raw,'$.request_id')`,
	},
	fallback: func(field string) string {
		return fmt.Sprintf("JSON_VALUE(raw,'$.%s')", field)
	},
}

// Raw makes no structural assumptions: friendly fields resolve to top-level
// JSON keys and `raw` is available verbatim.
var Raw = Profile{
	Name: "raw",
	fields: map[string]string{
		"level":   `JSON_VALUE(raw,'$.level')`,
		"message": `JSON_VALUE(raw,'$.message')`,
	},
	fallback: func(field string) string {
		return fmt.Sprintf("JSON_VALUE(raw,'$.%s')", field)
	},
}

var profiles = map[string]Profile{
	Vercel.Name: Vercel,
	Render.Name: Render,
	Raw.Name:    Raw,
}

// Get returns the named profile, or false if no such profile exists.
func Get(name string) (Profile, bool) {
	p, ok := profiles[name]
	return p, ok
}

// Names lists the built-in profile names, sorted.
func Names() []string {
	out := make([]string, 0, len(profiles))
	for n := range profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DetectFromPlatform picks a profile for a source's Telemetry `platform`
// attribute. Better Stack reports variants like "vercel_integration", so match
// liberally; unknown platforms fall back to the raw (passthrough) profile.
func DetectFromPlatform(platform string) string {
	p := strings.ToLower(platform)
	switch {
	case strings.Contains(p, "vercel"):
		return Vercel.Name
	case strings.Contains(p, "render"):
		return Render.Name
	default:
		return Raw.Name
	}
}

// Expr returns the SQL expression that yields the given friendly field. Real
// ClickHouse columns are returned as-is; known fields use the profile mapping;
// anything else uses the profile fallback. The bool is false only when the
// field name is unsafe to interpolate.
func (p Profile) Expr(field string) (string, bool) {
	if realColumns[field] {
		return field, true
	}
	if !safeField.MatchString(field) {
		return "", false
	}
	if expr, ok := p.fields[field]; ok {
		return expr, true
	}
	return p.fallback(field), true
}

// Projection returns "<expr> AS <field>" for a SELECT list. Real columns are
// emitted bare (no alias needed).
func (p Profile) Projection(field string) (string, bool) {
	expr, ok := p.Expr(field)
	if !ok {
		return "", false
	}
	if realColumns[field] {
		return expr, true
	}
	return fmt.Sprintf("%s AS %s", expr, field), true
}

// Map returns the friendly-name -> SQL-expression mappings (including the dt
// real column), for `betterheap schema`.
func (p Profile) Map() map[string]string {
	out := map[string]string{"dt": "dt"}
	for k, v := range p.fields {
		out[k] = v
	}
	return out
}

// Known lists the field names the profile maps explicitly (plus implicit real
// columns), sorted — used by `betterheap schema` and help text.
func (p Profile) Known() []string {
	out := []string{"dt"}
	for f := range p.fields {
		out = append(out, f)
	}
	out = append(out, "raw")
	sort.Strings(out)
	return out
}
