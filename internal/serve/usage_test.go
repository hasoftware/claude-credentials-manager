package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReportMonthBoundaryAndConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "session.ndjson")
	before := `{"ts":"2026-10-31T16:59:59Z","model":"A","in":5,"out":2}`
	after := `{"ts":"2026-10-31T17:00:00Z","model":"B","in":3,"cr":7,"out":2}`
	if err := os.WriteFile(file, []byte(before+"\n"+after), 0600); err != nil {
		t.Fatal(err)
	}
	var index usageIndex
	now := time.Date(2026, 11, 1, 1, 0, 0, 0, reportingZone)
	verify := func(month string, want tokenCounts) tokenReport {
		t.Helper()
		r, err := index.report(dir, month, now)
		if err != nil || r.MonthTotal != want {
			t.Fatalf("report %s: %+v %v", month, r, err)
		}
		return r
	}
	verify("2026-10", tokenCounts{Input: 5, Output: 2, Requests: 1})
	verify("2026-11", tokenCounts{})
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	want := tokenCounts{Input: 3, Output: 2, CacheRead: 7, Requests: 1}
	r := verify("2026-11", want)
	if r.FirstDay != "2026-10-31" || r.Today != want || r.Models["B"] != want || r.Days["2026-11-01"] != want {
		t.Fatal(r)
	}
	verify("2026-11", want)
}
