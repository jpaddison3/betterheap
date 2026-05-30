package cli

import (
	"strings"
	"testing"

	"github.com/jpaddison3/betterheap/internal/envelope"
	"github.com/jpaddison3/betterheap/internal/routing"
	"github.com/jpaddison3/betterheap/internal/source"
)

var tmplSrc = &source.Source{TeamID: 1, TableName: "x"}

func livePlan() *routing.Plan {
	return &routing.Plan{Tiers: []routing.TierQuery{{
		Tier: "live", Table: "remote(t1_x_logs)", Where: []string{"dt >= 'X'"},
	}}}
}

func TestExpandTemplate(t *testing.T) {
	out, err := expandTemplate("SELECT {level} AS lvl FROM {logs} WHERE {message} ILIKE '%a%'", tmplSrc, envelope.Vercel, livePlan())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "vercel.level") {
		t.Errorf("{level} not expanded: %s", out)
	}
	if !strings.Contains(out, "(SELECT dt, raw FROM remote(t1_x_logs) WHERE dt >= 'X')") {
		t.Errorf("{logs} not expanded: %s", out)
	}
	if !strings.Contains(out, "$.message") {
		t.Errorf("{message} not expanded: %s", out)
	}
	if strings.Contains(out, "{") {
		t.Errorf("unexpanded token remains: %s", out)
	}
}

func TestExpandTemplateLiveArchive(t *testing.T) {
	out, err := expandTemplate("SELECT 1 FROM {live}; {archive}", tmplSrc, envelope.Vercel, livePlan())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "remote(t1_x_logs)") || !strings.Contains(out, "s3Cluster(primary, t1_x_s3)") {
		t.Errorf("live/archive not expanded: %s", out)
	}
}

func TestExpandTemplateUnknownToken(t *testing.T) {
	if _, err := expandTemplate("SELECT {bogus} FROM {logs}", tmplSrc, envelope.Vercel, livePlan()); err == nil {
		t.Error("expected error for unknown token")
	}
}

func TestLogsRelationBothTiers(t *testing.T) {
	plan := livePlan()
	plan.Tiers = append(plan.Tiers, routing.TierQuery{Tier: "archive", Table: "s3Cluster(primary, t1_x_s3)", Where: []string{"_row_type = 1"}})
	rel := logsRelation(plan)
	if !strings.Contains(rel, "UNION ALL") {
		t.Errorf("expected UNION ALL in both-tier relation: %s", rel)
	}
}

func TestRemoveString(t *testing.T) {
	got := removeString([]string{"dt", "source", "level"}, "source")
	if len(got) != 2 || got[0] != "dt" || got[1] != "level" {
		t.Errorf("removeString = %v", got)
	}
}
