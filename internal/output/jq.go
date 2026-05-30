package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"

	"github.com/jpaddison3/betterheap/internal/client"
)

// JQSink runs an embedded gojq program over each row and emits the results.
// Output is ndjson (one result per line) unless format is json (a single
// array), so `--jq` composes with pipes without an external jq binary.
type JQSink struct {
	code   *gojq.Code
	w      io.Writer
	asJSON bool
	first  bool
	opened bool
}

// NewJQSink compiles program and returns a sink writing to w.
func NewJQSink(w io.Writer, program string, format Format) (*JQSink, error) {
	q, err := gojq.Parse(program)
	if err != nil {
		return nil, fmt.Errorf("--jq parse: %w", err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("--jq compile: %w", err)
	}
	return &JQSink{code: code, w: w, asJSON: format == FormatJSON, first: true}, nil
}

// Write runs the program against one row and emits each produced value.
func (j *JQSink) Write(row client.Row) error {
	// Normalize json.Number etc. to plain JSON types gojq understands.
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	var input any
	if err := json.Unmarshal(b, &input); err != nil {
		return err
	}
	iter := j.code.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			return fmt.Errorf("--jq: %w", err)
		}
		if err := j.emit(v); err != nil {
			return err
		}
	}
	return nil
}

func (j *JQSink) emit(v any) error {
	out, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if j.asJSON {
		if !j.opened {
			if _, err := io.WriteString(j.w, "[\n"); err != nil {
				return err
			}
			j.opened = true
		}
		sep := ",\n"
		if j.first {
			sep = ""
			j.first = false
		}
		_, err = fmt.Fprintf(j.w, "%s  %s", sep, out)
		return err
	}
	_, err = fmt.Fprintf(j.w, "%s\n", out)
	return err
}

// Close finishes a json array, if one was opened.
func (j *JQSink) Close() error {
	if !j.asJSON {
		return nil
	}
	if !j.opened {
		_, err := io.WriteString(j.w, "[]\n")
		return err
	}
	_, err := io.WriteString(j.w, "\n]\n")
	return err
}
