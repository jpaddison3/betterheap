// Package query assembles a tier-aware ClickHouse SELECT from a routing plan and
// an envelope profile, then executes it. It turns friendly filters (--level,
// --module, --search, --where) into envelope-aware, injection-safe SQL.
package query

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/envelope"
	"github.com/jpaddison3/betterheap/internal/routing"
	"github.com/jpaddison3/betterheap/internal/source"
)

// Filter is the set of friendly predicates a query can apply.
type Filter struct {
	Level     string   // exact level match (e.g. "error")
	Module    string   // exact module match
	Search    string   // case-insensitive substring over message
	RequestID string   // exact request_id match (for trace)
	Where     []string // raw "key<op>value" clauses, op in = != > < >= <=
}

// Spec is everything needed to build and run one query.
type Spec struct {
	Source  *source.Source
	Profile envelope.Profile
	Plan    *routing.Plan
	Fields  []string // output columns, in order
	Filter  Filter
	Limit   int
	Desc    bool // ORDER BY dt DESC when true, ASC otherwise
}

var whereClause = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(>=|<=|!=|=|>|<)\s*(.*)$`)
var numericLiteral = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// quoteCH renders a ClickHouse single-quoted string literal, escaping
// backslashes and quotes.
func quoteCH(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// BuildSQL renders the full SQL for a spec. Every tier in the plan becomes a
// bounded inner SELECT; multiple tiers are UNION ALL'd and re-ordered/limited by
// an outer SELECT so cross-tier results merge correctly.
func BuildSQL(spec Spec) (string, error) {
	if len(spec.Plan.Tiers) == 0 {
		return "", fmt.Errorf("routing produced no tiers to query")
	}

	// Output fields (deterministic order); ensure dt is available for ORDER BY.
	out := spec.Fields
	if len(out) == 0 {
		out = []string{"dt", "level", "module", "message"}
	}
	inner := ensureDT(out)

	innerProj := make([]string, 0, len(inner))
	for _, f := range inner {
		p, ok := spec.Profile.Projection(f)
		if !ok {
			return "", fmt.Errorf("invalid field name %q", f)
		}
		innerProj = append(innerProj, p)
	}

	filterPreds, err := buildFilter(spec.Profile, spec.Filter)
	if err != nil {
		return "", err
	}

	order := "ASC"
	if spec.Desc {
		order = "DESC"
	}

	selects := make([]string, 0, len(spec.Plan.Tiers))
	for _, t := range spec.Plan.Tiers {
		preds := append(append([]string{}, t.Where...), filterPreds...)
		where := ""
		if len(preds) > 0 {
			where = " WHERE " + strings.Join(preds, " AND ")
		}
		s := fmt.Sprintf("SELECT %s FROM %s%s ORDER BY dt %s LIMIT %d",
			strings.Join(innerProj, ", "), t.Table, where, order, spec.Limit)
		selects = append(selects, "("+s+")")
	}

	body := strings.Join(selects, "\nUNION ALL\n")
	outCols := strings.Join(out, ", ")
	return fmt.Sprintf("SELECT %s FROM (\n%s\n) ORDER BY dt %s LIMIT %d",
		outCols, body, order, spec.Limit), nil
}

// ensureDT returns fields with dt guaranteed present (appended if missing) so
// the outer ORDER BY dt always resolves.
func ensureDT(fields []string) []string {
	for _, f := range fields {
		if f == "dt" {
			return fields
		}
	}
	return append([]string{"dt"}, fields...)
}

// buildFilter turns friendly filters into envelope-aware WHERE predicates.
func buildFilter(p envelope.Profile, f Filter) ([]string, error) {
	var preds []string

	if f.Level != "" {
		expr, ok := p.Expr("level")
		if !ok {
			return nil, fmt.Errorf("profile %s has no level mapping", p.Name)
		}
		preds = append(preds, fmt.Sprintf("%s = %s", expr, quoteCH(f.Level)))
	}
	if f.Module != "" {
		expr, _ := p.Expr("module")
		preds = append(preds, fmt.Sprintf("%s = %s", expr, quoteCH(f.Module)))
	}
	if f.Search != "" {
		expr, _ := p.Expr("message")
		preds = append(preds, fmt.Sprintf("%s ILIKE %s", expr, quoteCH("%"+f.Search+"%")))
	}
	if f.RequestID != "" {
		expr, _ := p.Expr("request_id")
		preds = append(preds, fmt.Sprintf("%s = %s", expr, quoteCH(f.RequestID)))
	}
	for _, w := range f.Where {
		pred, err := parseWhere(p, w)
		if err != nil {
			return nil, err
		}
		preds = append(preds, pred)
	}
	return preds, nil
}

// parseWhere converts "status=500" / "env=production" / "status>=400" into an
// envelope-aware predicate. Bare-numeric values are emitted unquoted.
func parseWhere(p envelope.Profile, clause string) (string, error) {
	m := whereClause.FindStringSubmatch(clause)
	if m == nil {
		return "", fmt.Errorf("invalid --where %q (want key=value, key!=value, key>=N, ...)", clause)
	}
	key, op, val := m[1], m[2], strings.TrimSpace(m[3])
	expr, ok := p.Expr(key)
	if !ok {
		return "", fmt.Errorf("invalid --where field %q", key)
	}
	val = strings.Trim(val, `"'`)
	rhs := quoteCH(val)
	if numericLiteral.MatchString(val) {
		rhs = val
	}
	return fmt.Sprintf("%s %s %s", expr, op, rhs), nil
}

// statDimension resolves a --by dimension to (column name, SQL expression).
func statDimension(p envelope.Profile, by string) (string, string, error) {
	switch by {
	case "day":
		return "day", "toDate(dt)", nil
	case "hour":
		return "hour", "toStartOfHour(dt)", nil
	case "":
		by = "level"
	}
	expr, ok := p.Expr(by)
	if !ok {
		return "", "", fmt.Errorf("invalid --by field %q", by)
	}
	return by, expr, nil
}

// BuildStatsSQL builds a tier-aware aggregate: row counts grouped by a
// dimension, re-summed across tiers and ordered by count desc. It returns the
// SQL and the name of the grouping column.
func BuildStatsSQL(plan *routing.Plan, p envelope.Profile, filter Filter, by string, limit int) (string, string, error) {
	if len(plan.Tiers) == 0 {
		return "", "", fmt.Errorf("routing produced no tiers to query")
	}
	col, expr, err := statDimension(p, by)
	if err != nil {
		return "", "", err
	}
	filterPreds, err := buildFilter(p, filter)
	if err != nil {
		return "", "", err
	}
	if limit <= 0 {
		limit = 50
	}
	selects := make([]string, 0, len(plan.Tiers))
	for _, t := range plan.Tiers {
		preds := append(append([]string{}, t.Where...), filterPreds...)
		where := ""
		if len(preds) > 0 {
			where = " WHERE " + strings.Join(preds, " AND ")
		}
		selects = append(selects, fmt.Sprintf("(SELECT %s AS k, count() AS n FROM %s%s GROUP BY k)", expr, t.Table, where))
	}
	sql := fmt.Sprintf("SELECT k AS %s, sum(n) AS count FROM (\n%s\n) GROUP BY k ORDER BY count DESC LIMIT %d",
		col, strings.Join(selects, "\nUNION ALL\n"), limit)
	return sql, col, nil
}

// Result holds executed-query output plus per-tier diagnostics for --explain.
type Result struct {
	Rows     []client.Row
	SQL      string
	RowCount int
}

// Run builds and executes the query, streaming rows through fn. It returns the
// generated SQL (for --explain) and the row count.
func Run(ctx context.Context, qc *client.QueryClient, spec Spec, fn func(client.Row) error) (string, int, error) {
	sql, err := BuildSQL(spec)
	if err != nil {
		return "", 0, err
	}
	n, err := qc.Stream(ctx, sql, fn)
	return sql, n, err
}
