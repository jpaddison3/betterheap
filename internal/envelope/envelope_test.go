package envelope

import "testing"

func TestProjectionRealColumn(t *testing.T) {
	got, ok := Vercel.Projection("dt")
	if !ok || got != "dt" {
		t.Fatalf("dt projection = %q,%v; want \"dt\",true", got, ok)
	}
}

func TestProjectionMappedField(t *testing.T) {
	got, ok := Vercel.Projection("message")
	want := `JSON_VALUE(raw,'$.message') AS message`
	if !ok || got != want {
		t.Fatalf("message projection = %q; want %q", got, want)
	}
}

func TestExprLevelCoalesce(t *testing.T) {
	got, ok := Vercel.Expr("level")
	if !ok {
		t.Fatal("level expr not ok")
	}
	if !contains(got, "vercel.level") || !contains(got, "COALESCE") {
		t.Fatalf("level expr missing coalesce/vercel.level: %q", got)
	}
}

func TestVercelRequestIDAndStatus(t *testing.T) {
	// Ground-truthed from live Vercel data: request_id lives under
	// $.vercel.request_id and status under $.vercel.{proxy.status_code,statusCode}.
	rid, ok := Vercel.Expr("request_id")
	if !ok || !contains(rid, "vercel.request_id") {
		t.Errorf("request_id expr = %q", rid)
	}
	st, ok := Vercel.Expr("status")
	if !ok || !contains(st, "statusCode") {
		t.Errorf("status expr = %q", st)
	}
}

func TestExprUnknownFieldFallsBackToMessageJSON(t *testing.T) {
	got, ok := Vercel.Expr("jobId")
	want := `JSON_VALUE(raw,'$.message_json.jobId')`
	if !ok || got != want {
		t.Fatalf("fallback expr = %q; want %q", got, want)
	}
}

func TestExprRejectsUnsafeField(t *testing.T) {
	if _, ok := Vercel.Expr("bad-name"); ok {
		t.Fatal("expected unsafe field name to be rejected")
	}
	if _, ok := Vercel.Expr("a'); DROP"); ok {
		t.Fatal("expected injection attempt to be rejected")
	}
}

func TestDetectFromPlatform(t *testing.T) {
	cases := map[string]string{
		"vercel":             "vercel",
		"vercel_integration": "vercel",
		"render":             "render",
		"nginx":              "raw",
		"":                   "raw",
	}
	for in, want := range cases {
		if got := DetectFromPlatform(in); got != want {
			t.Errorf("DetectFromPlatform(%q) = %q; want %q", in, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
