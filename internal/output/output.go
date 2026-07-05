// Package output renders query rows in the format the caller (or the terminal)
// wants, keeping data on stdout and diagnostics on stderr. Field order is
// honored exactly so agents get deterministic columns.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/jpaddison3/betterheap/internal/client"
)

// Format is an output encoding.
type Format string

const (
	FormatJSON   Format = "json"
	FormatNDJSON Format = "ndjson"
	FormatTable  Format = "table"
	FormatCSV    Format = "csv"
	FormatPretty Format = "pretty"
)

// IsTTY reports whether w is an interactive terminal.
func IsTTY(w *os.File) bool {
	return term.IsTerminal(int(w.Fd()))
}

// ResolveFormat picks a format: an explicit choice wins; otherwise pretty on a
// terminal and ndjson when piped (so `| jq` just works).
func ResolveFormat(flag string, isTTY bool) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(flag))) {
	case "":
		if isTTY {
			return FormatPretty, nil
		}
		return FormatNDJSON, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatNDJSON:
		return FormatNDJSON, nil
	case FormatTable:
		return FormatTable, nil
	case FormatCSV:
		return FormatCSV, nil
	case FormatPretty:
		return FormatPretty, nil
	default:
		return "", fmt.Errorf("invalid --format %q (want json|ndjson|table|csv|pretty)", flag)
	}
}

// TruncateLimit caps the rune length of any single string field in row output
// (unless --full is passed). A log line's raw JSON or a stack trace can run to
// many KB; capping it keeps a `logs -n 100` dump from flooding an agent's
// context. Server-side filtering is unaffected — this is display only.
const TruncateLimit = 2000

// Sink consumes rows in a chosen format. Write may stream or buffer; Close
// flushes buffered formats. Construct with NewSink.
type Sink struct {
	w      io.Writer
	fields []string
	format Format
	color  bool
	full   bool // when true, never truncate long string values

	// streaming state
	csvw       *csv.Writer
	jsonFirst  bool
	jsonOpened bool
	// buffered state (table/pretty)
	rows [][]string
}

// ColorEnabled reports whether color should be used given the format target and
// environment (honors NO_COLOR and --no-color).
func ColorEnabled(noColor bool, isTTY bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY
}

// NewSink builds a Sink writing to w. fields is the ordered column set. When
// full is true, long string values are emitted verbatim; otherwise they are
// truncated to TruncateLimit runes with an inline marker.
func NewSink(w io.Writer, format Format, fields []string, color, full bool) *Sink {
	s := &Sink{w: w, fields: fields, format: format, color: color, full: full, jsonFirst: true}
	switch format {
	case FormatCSV:
		s.csvw = csv.NewWriter(w)
		_ = s.csvw.Write(fields)
	case FormatTable, FormatPretty:
		// header is rendered at Close once widths are known
	}
	return s
}

// Write emits or buffers one row.
func (s *Sink) Write(row client.Row) error {
	if !s.full {
		row = truncateRow(s.fields, row)
	}
	switch s.format {
	case FormatNDJSON:
		b, err := marshalOrdered(s.fields, row)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(s.w, "%s\n", b)
		return err
	case FormatJSON:
		if !s.jsonOpened {
			if _, err := io.WriteString(s.w, "[\n"); err != nil {
				return err
			}
			s.jsonOpened = true
		}
		b, err := marshalOrdered(s.fields, row)
		if err != nil {
			return err
		}
		sep := ",\n"
		if s.jsonFirst {
			sep = ""
			s.jsonFirst = false
		}
		_, err = fmt.Fprintf(s.w, "%s  %s", sep, b)
		return err
	case FormatCSV:
		s.csvw.Write(cells(s.fields, row))
		return s.csvw.Error()
	case FormatTable, FormatPretty:
		s.rows = append(s.rows, cells(s.fields, row))
		return nil
	}
	return fmt.Errorf("unknown format %q", s.format)
}

// Close flushes buffered output.
func (s *Sink) Close() error {
	switch s.format {
	case FormatJSON:
		if !s.jsonOpened {
			_, err := io.WriteString(s.w, "[]\n")
			return err
		}
		_, err := io.WriteString(s.w, "\n]\n")
		return err
	case FormatCSV:
		s.csvw.Flush()
		return s.csvw.Error()
	case FormatTable:
		return s.flushColumns(false)
	case FormatPretty:
		return s.flushColumns(s.color)
	}
	return nil
}

// flushColumns renders buffered rows as aligned columns, optionally colorizing
// the level column.
func (s *Sink) flushColumns(color bool) error {
	widths := make([]int, len(s.fields))
	for i, f := range s.fields {
		widths[i] = len(f)
	}
	for _, r := range s.rows {
		for i, c := range r {
			if w := displayWidth(c); w > widths[i] {
				widths[i] = w
			}
		}
	}

	var b strings.Builder
	for i, f := range s.fields {
		b.WriteString(pad(strings.ToUpper(f), widths[i]))
		if i < len(s.fields)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")
	levelIdx := indexOf(s.fields, "level")
	for _, r := range s.rows {
		for i, c := range r {
			cell := pad(c, widths[i])
			if color && i == levelIdx {
				cell = colorizeLevel(c, pad(c, widths[i]))
			}
			b.WriteString(cell)
			if i < len(r)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(s.w, b.String())
	return err
}

// marshalOrdered renders a row as a JSON object with keys in field order.
func marshalOrdered(fields []string, row client.Row) ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(f)
		b.Write(key)
		b.WriteByte(':')
		v, err := json.Marshal(row[f])
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

func cells(fields []string, row client.Row) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = cell(row[f])
	}
	return out
}

// truncateRow returns row with any oversized string field (among fields)
// shortened. It copies lazily: if nothing is truncated, the original row is
// returned unchanged and the caller's map is never mutated.
func truncateRow(fields []string, row client.Row) client.Row {
	var out client.Row
	for _, f := range fields {
		s, ok := row[f].(string)
		if !ok {
			continue
		}
		t, did := truncateString(s)
		if !did {
			continue
		}
		if out == nil {
			out = make(client.Row, len(row))
			for k, v := range row {
				out[k] = v
			}
		}
		out[f] = t
	}
	if out == nil {
		return row
	}
	return out
}

// truncateString shortens s to TruncateLimit runes (UTF-8 safe) with an inline
// marker, returning whether it changed. The byte-length fast path is sound
// because a rune is at least one byte, so len(s) <= limit ⇒ rune count <= limit.
func truncateString(s string) (string, bool) {
	if len(s) <= TruncateLimit {
		return s, false
	}
	r := []rune(s)
	if len(r) <= TruncateLimit {
		return s, false
	}
	dropped := len(r) - TruncateLimit
	return fmt.Sprintf("%s…[truncated %d chars — use --full]", string(r[:TruncateLimit]), dropped), true
}

// cell renders a single value as text for table/csv/pretty output.
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(t)
	}
}

func pad(s string, w int) string {
	if displayWidth(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-displayWidth(s))
}

// displayWidth is len in runes (good enough for alignment of ASCII-ish logs).
func displayWidth(s string) int { return len([]rune(s)) }

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

// ANSI colors for the pretty level column.
const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiDim    = "\033[2m"
)

// Line renders a row as one compact, space-separated line for streaming output
// (tail -f), coloring the level field when color is enabled. Long string values
// are truncated unless full is set.
func Line(fields []string, row client.Row, color, full bool) string {
	if !full {
		row = truncateRow(fields, row)
	}
	li := indexOf(fields, "level")
	parts := make([]string, len(fields))
	for i, f := range fields {
		c := cell(row[f])
		if color && i == li {
			c = colorizeLevel(c, c)
		}
		parts[i] = c
	}
	return strings.Join(parts, "  ")
}

func colorizeLevel(level, padded string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "err", "fatal", "critical":
		return ansiRed + padded + ansiReset
	case "warning", "warn":
		return ansiYellow + padded + ansiReset
	case "info", "debug", "trace":
		return ansiDim + padded + ansiReset
	default:
		return padded
	}
}
