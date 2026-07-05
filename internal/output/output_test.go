package output

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jpaddison3/betterheap/internal/client"
)

func TestMarshalOrderedPreservesFieldOrder(t *testing.T) {
	row := client.Row{"a": 1, "b": "two", "c": nil}
	b, err := marshalOrdered([]string{"c", "a", "b"}, row)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"c":null,"a":1,"b":"two"}`; got != want {
		t.Errorf("marshalOrdered = %s; want %s", got, want)
	}
}

func TestResolveFormat(t *testing.T) {
	if f, _ := ResolveFormat("", true); f != FormatPretty {
		t.Errorf("tty default = %s; want pretty", f)
	}
	if f, _ := ResolveFormat("", false); f != FormatNDJSON {
		t.Errorf("piped default = %s; want ndjson", f)
	}
	if f, _ := ResolveFormat("json", true); f != FormatJSON {
		t.Errorf("explicit json = %s", f)
	}
	if _, err := ResolveFormat("bogus", true); err == nil {
		t.Error("expected error for bogus format")
	}
}

func TestCell(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{json.Number("5"), "5"},
		{true, "true"},
		{false, "false"},
		{"hello", "hello"},
	}
	for _, c := range cases {
		if got := cell(c.in); got != c.want {
			t.Errorf("cell(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSinkNDJSONFiltersToFields(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, FormatNDJSON, []string{"dt", "level"}, false, false)
	if err := s.Write(client.Row{"dt": "t1", "level": "error", "extra": "ignored"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "{\"dt\":\"t1\",\"level\":\"error\"}\n"; got != want {
		t.Errorf("ndjson = %q; want %q", got, want)
	}
}

func TestSinkCSVQuotesCommas(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, FormatCSV, []string{"a", "b"}, false, false)
	_ = s.Write(client.Row{"a": "1", "b": "x,y"})
	_ = s.Close()
	if got, want := buf.String(), "a,b\n1,\"x,y\"\n"; got != want {
		t.Errorf("csv = %q; want %q", got, want)
	}
}

func TestJQSinkScalar(t *testing.T) {
	var buf bytes.Buffer
	js, err := NewJQSink(&buf, ".level", FormatNDJSON)
	if err != nil {
		t.Fatal(err)
	}
	_ = js.Write(client.Row{"level": "error", "message": "x"})
	_ = js.Close()
	if got := buf.String(); got != "\"error\"\n" {
		t.Errorf("jq scalar = %q; want \"error\"", got)
	}
}

func TestJQSinkObject(t *testing.T) {
	var buf bytes.Buffer
	js, err := NewJQSink(&buf, "{lvl: .level}", FormatNDJSON)
	if err != nil {
		t.Fatal(err)
	}
	_ = js.Write(client.Row{"level": "error"})
	_ = js.Close()
	if got := buf.String(); got != "{\"lvl\":\"error\"}\n" {
		t.Errorf("jq object = %q", got)
	}
}

func TestJQSinkBadProgram(t *testing.T) {
	if _, err := NewJQSink(nil, "this is | not valid (", FormatNDJSON); err == nil {
		t.Error("expected parse error for bad jq program")
	}
}

func TestSinkJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, FormatJSON, []string{"a"}, false, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "[]\n" {
		t.Errorf("empty json = %q; want []", got)
	}
}

func TestTruncateStringShort(t *testing.T) {
	if got, did := truncateString("hello"); did || got != "hello" {
		t.Errorf("short string changed: got %q did=%v", got, did)
	}
}

func TestTruncateStringLongASCII(t *testing.T) {
	in := strings.Repeat("x", TruncateLimit+50)
	got, did := truncateString(in)
	if !did {
		t.Fatal("expected truncation")
	}
	want := strings.Repeat("x", TruncateLimit) + "…[truncated 50 chars — use --full]"
	if got != want {
		t.Errorf("truncateString = %q; want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Error("truncated output is not valid UTF-8")
	}
}

func TestTruncateStringMultibyteBoundary(t *testing.T) {
	// U+20AC is 3 bytes but 1 rune; a byte-based cut would split a rune.
	in := strings.Repeat("€", TruncateLimit+10)
	got, did := truncateString(in)
	if !did {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(got) {
		t.Errorf("multibyte truncation split a rune: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("€", TruncateLimit)) {
		t.Error("head is not the first TruncateLimit runes")
	}
	if !strings.Contains(got, "truncated 10 chars") {
		t.Errorf("marker missing/incorrect: %q", got)
	}
}

func TestSinkTruncatesLongField(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("a", TruncateLimit+5)
	s := NewSink(&buf, FormatNDJSON, []string{"message"}, false, false)
	_ = s.Write(client.Row{"message": long})
	_ = s.Close()
	out := buf.String()
	if strings.Contains(out, long) {
		t.Error("full value leaked despite truncation")
	}
	if !strings.Contains(out, "truncated 5 chars") {
		t.Errorf("marker missing: %q", out)
	}
}

func TestSinkFullSkipsTruncation(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("a", TruncateLimit+5)
	s := NewSink(&buf, FormatNDJSON, []string{"message"}, false, true)
	_ = s.Write(client.Row{"message": long})
	_ = s.Close()
	if !strings.Contains(buf.String(), long) {
		t.Error("--full should emit the full value verbatim")
	}
}

func TestTruncateRowDoesNotMutateInput(t *testing.T) {
	long := strings.Repeat("a", TruncateLimit+5)
	row := client.Row{"message": long, "level": "error"}
	out := truncateRow([]string{"message", "level"}, row)
	if row["message"] != long {
		t.Error("truncateRow mutated the caller's row")
	}
	if out["message"] == long {
		t.Error("truncateRow did not truncate the output")
	}
}

func TestTruncateRowReturnsOriginalWhenNoChange(t *testing.T) {
	row := client.Row{"level": "error", "dt": "t1"}
	out := truncateRow([]string{"level", "dt"}, row)
	if reflect.ValueOf(out).Pointer() != reflect.ValueOf(row).Pointer() {
		t.Error("expected the original map back when nothing truncates (no needless copy)")
	}
}
