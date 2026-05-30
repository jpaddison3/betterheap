package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// stderr is the diagnostics sink for the client package; overridable in tests.
var stderr io.Writer = os.Stderr

const telemetryBaseURL = "https://telemetry.betterstack.com/api/v1"

// TelemetryClient talks to the Better Stack Telemetry API for source discovery.
type TelemetryClient struct {
	BaseURL string
	Token   string
	Verbose bool
	HTTP    *http.Client
}

// NewTelemetryClient builds a Telemetry API client from a bearer token.
func NewTelemetryClient(token string, verbose bool) *TelemetryClient {
	return &TelemetryClient{
		BaseURL: telemetryBaseURL,
		Token:   token,
		Verbose: verbose,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Source is a Better Stack telemetry source (a log table with two storage
// tiers). Fields mirror the Telemetry API `attributes` object.
type Source struct {
	ID              string
	TeamID          int
	TeamName        string
	Name            string
	TableName       string
	Platform        string
	IngestingHost   string
	IngestingPaused bool
	LogsRetention   int
	CreatedAt       string
}

// sourcesResponse is the JSON:API envelope returned by the sources endpoint.
type sourcesResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			TeamID          int    `json:"team_id"`
			TeamName        string `json:"team_name"`
			Name            string `json:"name"`
			TableName       string `json:"table_name"`
			Platform        string `json:"platform"`
			IngestingHost   string `json:"ingesting_host"`
			IngestingPaused bool   `json:"ingesting_paused"`
			LogsRetention   int    `json:"logs_retention"`
			CreatedAt       string `json:"created_at"`
		} `json:"attributes"`
	} `json:"data"`
}

// APIError is a non-2xx response from the Telemetry API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("telemetry api: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("telemetry api: HTTP %d: %s", e.StatusCode, e.Body)
}

// ListSources returns every source visible to the token, following pagination.
func (c *TelemetryClient) ListSources(ctx context.Context) ([]Source, error) {
	var out []Source
	const perPage = 50
	const maxPages = 100 // safety bound against pagination loops
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(perPage))
		resp, err := c.get(ctx, "/sources?"+q.Encode())
		if err != nil {
			return nil, err
		}
		if len(resp.Data) == 0 {
			break
		}
		for _, d := range resp.Data {
			a := d.Attributes
			out = append(out, Source{
				ID:              d.ID,
				TeamID:          a.TeamID,
				TeamName:        a.TeamName,
				Name:            a.Name,
				TableName:       a.TableName,
				Platform:        a.Platform,
				IngestingHost:   a.IngestingHost,
				IngestingPaused: a.IngestingPaused,
				LogsRetention:   a.LogsRetention,
				CreatedAt:       a.CreatedAt,
			})
		}
		if len(resp.Data) < perPage {
			break
		}
	}
	return out, nil
}

func (c *TelemetryClient) get(ctx context.Context, path string) (*sourcesResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	if c.Verbose {
		fmt.Fprintf(stderr, "> GET %s%s\n", c.BaseURL, path)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if c.Verbose {
		fmt.Fprintf(stderr, "< %d %s\n", resp.StatusCode, resp.Status)
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	var out sourcesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode sources response: %w", err)
	}
	return &out, nil
}
