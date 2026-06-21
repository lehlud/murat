package ui

import (
	"strings"

	"lehnert.dev/murat/internal/store"
)

func (a *App) aggregateReportPreview(msg *store.Message) string {
	if a == nil || a.store == nil || msg == nil || !msg.HasAttachment {
		return ""
	}
	report, err := a.store.AggregateReport(msg)
	if err != nil || report == nil {
		return ""
	}
	return formatAggregateReport(report)
}

func formatAggregateReport(report *store.AggregateReport) string {
	if report == nil {
		return ""
	}
	lines := []string{"Aggregate report"}
	metaRows := [][]string{
		{"Type", report.Kind},
		{"Organization", report.Organization},
		{"Domain", report.Domain},
		{"Report ID", report.ReportID},
		{"Date range", report.DateRange},
	}
	lines = append(lines, formatReportTable([]string{"Field", "Value"}, metaRows, []int{12, 64})...)
	if len(report.Rows) == 0 {
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", "Records")
	rows := make([][]string, 0, len(report.Rows))
	for _, row := range report.Rows {
		rows = append(rows, []string{row.Source, row.Count, row.Policy, row.DKIM, row.SPF, row.Result, row.Detail})
	}
	lines = append(lines, formatReportTable([]string{"Source", "Count", "Policy", "DKIM", "SPF", "Result", "Detail"}, rows, []int{24, 8, 12, 18, 18, 18, 32})...)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatReportTable(headers []string, rows [][]string, caps []int) []string {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = displayLen(header)
	}
	for _, row := range rows {
		for i := range headers {
			value := ""
			if i < len(row) {
				value = strings.TrimSpace(row[i])
			}
			widths[i] = max(widths[i], displayLen(value))
		}
	}
	for i := range widths {
		if i < len(caps) && caps[i] > 0 {
			widths[i] = min(widths[i], caps[i])
		}
	}
	out := []string{formatReportTableRow(headers, widths), formatReportTableDivider(widths)}
	for _, row := range rows {
		out = append(out, formatReportTableRow(row, widths))
	}
	return out
}

func formatReportTableRow(values []string, widths []int) string {
	parts := make([]string, 0, len(widths))
	for i, width := range widths {
		value := ""
		if i < len(values) {
			value = strings.TrimSpace(values[i])
		}
		parts = append(parts, padRight(shorten(value, width), width))
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func formatReportTableDivider(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width))
	}
	return strings.Join(parts, "  ")
}
