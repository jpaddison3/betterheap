package source

import (
	"testing"

	"github.com/jpaddison3/betterheap/internal/client"
)

func TestTableNames(t *testing.T) {
	s := &Source{TeamID: 99999, TableName: "myapp"}
	if got := s.LiveTable(); got != "t99999_myapp_logs" {
		t.Errorf("LiveTable = %q", got)
	}
	if got := s.ArchiveTable(); got != "t99999_myapp_s3" {
		t.Errorf("ArchiveTable = %q", got)
	}
}

func TestRegionFromHost(t *testing.T) {
	cases := map[string]string{
		"s95.eu-nbg-2.betterstackdata.com":        "eu-nbg-2",
		"s1.us-chi-1.betterstackdata.com":         "us-chi-1",
		"s67890.eu-fsn-3-vec.betterstackdata.com": "eu-fsn-3", // drops the -vec ingest suffix
		"":        "",
		"garbage": "",
	}
	for in, want := range cases {
		if got := RegionFromHost(in); got != want {
			t.Errorf("RegionFromHost(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestFromClientDetectsProfileAndRegion(t *testing.T) {
	cs := client.Source{
		TeamID:        99999,
		Name:          "Myapp",
		TableName:     "myapp",
		Platform:      "vercel",
		IngestingHost: "s95.eu-nbg-2.betterstackdata.com",
		LogsRetention: 90,
	}
	got := fromClient(cs, "eu-nbg-2")
	if got.Profile != "vercel" {
		t.Errorf("profile = %q; want vercel", got.Profile)
	}
	if got.Region != "eu-nbg-2" {
		t.Errorf("region = %q; want eu-nbg-2", got.Region)
	}
	if got.Name != "myapp" {
		t.Errorf("name = %q; want myapp (table_name)", got.Name)
	}
}

func TestFromClientFallsBackToDefaultRegion(t *testing.T) {
	cs := client.Source{TeamID: 1, TableName: "x", Platform: "render", IngestingHost: "weird-host"}
	got := fromClient(cs, "ap-sin-1")
	if got.Region != "ap-sin-1" {
		t.Errorf("region = %q; want ap-sin-1 fallback", got.Region)
	}
	if got.Profile != "render" {
		t.Errorf("profile = %q; want render", got.Profile)
	}
}

func TestRegistryResolveUnknownSource(t *testing.T) {
	// Empty name short-circuits before any network call.
	r := NewRegistry(nil, "eu-nbg-2", "")
	if _, err := r.Resolve(nil, ""); err == nil {
		t.Error("expected error for empty source name")
	}
}
