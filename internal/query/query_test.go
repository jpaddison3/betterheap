package query

import (
	"strings"
	"testing"

	"github.com/jpaddison3/betterheap/internal/envelope"
	"github.com/jpaddison3/betterheap/internal/routing"
	"github.com/jpaddison3/betterheap/internal/source"
)

var src = &source.Source{TeamID: 1, TableName: "x"}

func liveSpec(f Filter, fields []string) Spec {
	return Spec{
		Source:  src,
		Profile: envelope.Vercel,
		Plan: &routing.Plan{Tiers: []routing.TierQuery{{
			Tier:  "live",
			Table: "remote(t1_x_logs)",
			Where: []string{"dt >= '2026-05-28 00:00:00'"},
		}}},
		Fields: fields,
		Filter: f,
		Limit:  50,
		Desc:   true,
	}
}

func TestBuildSQLSingleTierLevelExact(t *testing.T) {
	sql, err := BuildSQL(liveSpec(Filter{Level: "error"}, []string{"dt", "level", "message"}))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sql, "remote(t1_x_logs)")
	mustContain(t, sql, "AS level")
	mustContain(t, sql, "= 'error'") // exact match, not ILIKE
	mustContain(t, sql, "ORDER BY dt DESC LIMIT 50")
	mustContain(t, sql, "SELECT dt, level, message FROM (")
}

func TestBuildSQLSearchUsesILIKE(t *testing.T) {
	sql, err := BuildSQL(liveSpec(Filter{Search: "boom"}, []string{"dt", "message"}))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sql, "ILIKE '%boom%'")
}

func TestBuildSQLWhereNumericVsString(t *testing.T) {
	sql, err := BuildSQL(liveSpec(Filter{Where: []string{"status=500", "env=production"}}, []string{"dt"}))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sql, "= 500")          // numeric: unquoted
	mustContain(t, sql, "= 'production'") // string: quoted
	mustContain(t, sql, "status_code")    // status maps through envelope
	mustContain(t, sql, "environment")    // env maps through envelope
}

func TestBuildSQLTwoTierUnion(t *testing.T) {
	spec := liveSpec(Filter{Level: "error"}, []string{"dt", "level", "message"})
	spec.Plan.Tiers = append(spec.Plan.Tiers, routing.TierQuery{
		Tier:  "archive",
		Table: "s3Cluster(primary, t1_x_s3)",
		Where: []string{"_row_type = 1", "dt < '2026-05-28 00:00:00'"},
	})
	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sql, "UNION ALL")
	mustContain(t, sql, "remote(t1_x_logs)")
	mustContain(t, sql, "s3Cluster(primary, t1_x_s3)")
}

func TestBuildSQLInjectionInValueIsEscaped(t *testing.T) {
	sql, err := BuildSQL(liveSpec(Filter{Module: "a' OR '1'='1"}, []string{"dt"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "OR '1'='1'") && !strings.Contains(sql, `a\' OR`) {
		t.Fatalf("value not escaped: %s", sql)
	}
	mustContain(t, sql, `\'`)
}

func TestBuildSQLRejectsBadField(t *testing.T) {
	if _, err := BuildSQL(liveSpec(Filter{}, []string{"dt", "bad-field"})); err == nil {
		t.Fatal("expected error for invalid field name")
	}
}

func TestBuildSQLRequestID(t *testing.T) {
	sql, err := BuildSQL(liveSpec(Filter{RequestID: "abc-123"}, []string{"dt", "message"}))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sql, "request_id")
	mustContain(t, sql, "= 'abc-123'")
}

func statsPlan() *routing.Plan {
	return &routing.Plan{Tiers: []routing.TierQuery{{
		Tier: "live", Table: "remote(t1_x_logs)", Where: []string{"dt >= '2026-05-28 00:00:00'"},
	}}}
}

func TestBuildStatsSQLByModule(t *testing.T) {
	sql, col, err := BuildStatsSQL(statsPlan(), envelope.Vercel, Filter{Level: "error"}, "module", 10)
	if err != nil {
		t.Fatal(err)
	}
	if col != "module" {
		t.Errorf("col = %q; want module", col)
	}
	mustContain(t, sql, "message_json.module")
	mustContain(t, sql, "= 'error'")
	mustContain(t, sql, "GROUP BY k")
	mustContain(t, sql, "AS module")
	mustContain(t, sql, "ORDER BY count DESC LIMIT 10")
}

func TestBuildStatsSQLByDay(t *testing.T) {
	sql, col, err := BuildStatsSQL(statsPlan(), envelope.Vercel, Filter{}, "day", 30)
	if err != nil {
		t.Fatal(err)
	}
	if col != "day" {
		t.Errorf("col = %q; want day", col)
	}
	mustContain(t, sql, "toDate(dt)")
}

func TestBuildStatsSQLTwoTierUnion(t *testing.T) {
	plan := statsPlan()
	plan.Tiers = append(plan.Tiers, routing.TierQuery{Tier: "archive", Table: "s3Cluster(primary, t1_x_s3)", Where: []string{"_row_type = 1"}})
	sql, _, err := BuildStatsSQL(plan, envelope.Vercel, Filter{}, "level", 5)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sql, "UNION ALL")
	mustContain(t, sql, "sum(n) AS count")
}

func TestBuildStatsSQLInvalidBy(t *testing.T) {
	if _, _, err := BuildStatsSQL(statsPlan(), envelope.Vercel, Filter{}, "bad-field", 10); err == nil {
		t.Error("expected error for invalid --by field")
	}
}

func TestParseWhereInvalid(t *testing.T) {
	if _, err := parseWhere(envelope.Vercel, "no-operator-here"); err == nil {
		t.Error("expected error for malformed where clause")
	}
}

func TestQuoteCH(t *testing.T) {
	if got := quoteCH(`a'b\c`); got != `'a\'b\\c'` {
		t.Errorf("quoteCH = %q", got)
	}
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected SQL to contain %q\n--- SQL ---\n%s", sub, s)
	}
}
