package ui

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestFormatAggregateReport(t *testing.T) {
	text := formatAggregateReport(&store.AggregateReport{
		Kind:         "TLSRPT",
		Organization: "google.com",
		Domain:       "ludwig-lehnert.de",
		ReportID:     "report-1",
		DateRange:    "2026-06-20 - 2026-06-21",
		Rows: []store.AggregateReportRow{{
			Source: "mx.ludwig-lehnert.de",
			Count:  "12/1",
			Policy: "sts",
			Result: "failures",
			Detail: "success/failure",
		}},
	})
	for _, want := range []string{"Aggregate report", "Type", "TLSRPT", "Records", "mx.ludwig-lehnert.de", "12/1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report preview missing %q:\n%s", want, text)
		}
	}
}
