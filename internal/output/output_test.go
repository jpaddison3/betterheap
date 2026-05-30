package output

import (
	"bytes"
	"encoding/json"
	"testing"

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
	s := NewSink(&buf, FormatNDJSON, []string{"dt", "level"}, false)
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
	s := NewSink(&buf, FormatCSV, []string{"a", "b"}, false)
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
	s := NewSink(&buf, FormatJSON, []string{"a"}, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "[]\n" {
		t.Errorf("empty json = %q; want []", got)
	}
}
