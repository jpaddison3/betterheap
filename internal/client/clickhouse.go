// Package client talks to Better Stack's two HTTP surfaces: the
// ClickHouse-compatible Query API (log data) and the Telemetry API (source
// metadata).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Row is a single decoded result row from the Query API.
type Row map[string]any

// QueryClient is a read-only ClickHouse HTTP client for Better Stack's Query API.
type QueryClient struct {
	BaseURL  string
	Username string
	Password string
	Verbose  bool
	HTTP     *http.Client
}

// HostForRegion builds the Connect Remotely endpoint for a region, e.g.
// eu-nbg-2 -> https://eu-nbg-2-connect.betterstackdata.com.
func HostForRegion(region string) string {
	return fmt.Sprintf("https://%s-connect.betterstackdata.com", region)
}

// NewQueryClient builds a query client. host may be a full URL, a bare
// hostname, or empty (in which case region is used). region is only consulted
// when host is empty.
func NewQueryClient(host, region, username, password string, verbose bool) *QueryClient {
	base := strings.TrimRight(host, "/")
	switch {
	case base == "":
		base = HostForRegion(region)
	case !strings.Contains(base, "://"):
		base = "https://" + base
	}
	return &QueryClient{
		BaseURL:  base,
		Username: username,
		Password: password,
		Verbose:  verbose,
		HTTP:     &http.Client{Timeout: 120 * time.Second},
	}
}

// QueryError is a non-2xx response from the Query API; ClickHouse returns
// errors as a plain-text body.
type QueryError struct {
	StatusCode int
	Body       string
}

func (e *QueryError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 500 {
		body = body[:500] + "…"
	}
	if body == "" {
		return fmt.Sprintf("query api: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("query api: HTTP %d: %s", e.StatusCode, body)
}

// retriable reports whether a status code is worth retrying.
func retriable(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// Stream executes sql and invokes fn for each result row as it is decoded,
// without buffering the whole result set. It appends FORMAT JSONEachRow itself;
// sql must not already specify a FORMAT. Returns the number of rows seen.
func (c *QueryClient) Stream(ctx context.Context, sql string, fn func(Row) error) (int, error) {
	body := strings.TrimRight(strings.TrimSpace(sql), ";") + "\nFORMAT JSONEachRow"

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		n, retry, err := c.streamOnce(ctx, body, fn)
		if err == nil {
			return n, nil
		}
		lastErr = err
		// Don't retry once rows have been delivered to fn (the caller may have
		// already emitted them) or when the error isn't transient.
		if !retry || n > 0 || ctx.Err() != nil {
			return n, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Duration(200*(1<<(attempt-1))) * time.Millisecond):
		}
	}
	return 0, lastErr
}

// streamOnce performs a single attempt. retry reports whether lastErr is
// transient and the request may be safely retried.
func (c *QueryClient) streamOnce(ctx context.Context, body string, fn func(Row) error) (n int, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Content-Type", "text/plain")
	req.SetBasicAuth(c.Username, c.Password)

	if c.Verbose {
		fmt.Fprintf(stderr, "> POST %s\n", c.BaseURL)
		for _, line := range strings.Split(body, "\n") {
			fmt.Fprintf(stderr, ">   %s\n", line)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, true, err // network errors are retriable
	}
	defer resp.Body.Close()

	if c.Verbose {
		fmt.Fprintf(stderr, "< %d %s\n", resp.StatusCode, resp.Status)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return 0, retriable(resp.StatusCode), &QueryError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber() // preserve int/float fidelity for output
	for {
		var row Row
		if derr := dec.Decode(&row); derr != nil {
			if errors.Is(derr, io.EOF) {
				break
			}
			return n, false, fmt.Errorf("decode result stream: %w", derr)
		}
		if ferr := fn(row); ferr != nil {
			return n, false, ferr
		}
		n++
	}
	return n, false, nil
}

// Query executes sql and returns all rows. Prefer Stream for large result sets.
func (c *QueryClient) Query(ctx context.Context, sql string) ([]Row, error) {
	var rows []Row
	_, err := c.Stream(ctx, sql, func(r Row) error {
		rows = append(rows, r)
		return nil
	})
	return rows, err
}

// QueryScalar runs sql expected to return a single row/column and returns it as
// a string. Used for probes like SELECT min(dt).
func (c *QueryClient) QueryScalar(ctx context.Context, sql string) (string, error) {
	rows, err := c.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	for _, v := range rows[0] {
		return fmt.Sprint(v), nil
	}
	return "", nil
}

// RawBytes runs sql and returns the untouched response body, for the `sql`
// escape hatch where the caller picks the ClickHouse FORMAT.
func (c *QueryClient) RawBytes(ctx context.Context, sql string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/plain")
	req.SetBasicAuth(c.Username, c.Password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &QueryError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	return bytes.TrimRight(raw, "\n"), nil
}
