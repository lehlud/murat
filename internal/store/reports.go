package store

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const aggregateReportCategory = "dmarc"
const aggregateReportMaxBytes = 10 << 20

type AggregateReport struct {
	Category     string
	Kind         string
	Organization string
	Domain       string
	ReportID     string
	DateRange    string
	Rows         []AggregateReportRow
}

type AggregateReportRow struct {
	Source string
	Count  string
	Policy string
	DKIM   string
	SPF    string
	Result string
	Detail string
}

func (s *Store) ReportCategory(msg *Message) string {
	if msg == nil {
		return ""
	}
	category := cleanReportCategory(msg.ReportCategory)
	if category != "" || msg.ReportChecked || msg.RawRel == "" || !msg.HasAttachment {
		return category
	}
	report, err := s.AggregateReport(msg)
	if err != nil || report == nil {
		return ""
	}
	return cleanReportCategory(report.Category)
}

func (s *Store) AggregateReport(msg *Message) (*AggregateReport, error) {
	attachments, err := s.Attachments(msg)
	if err != nil {
		return nil, err
	}
	report := aggregateReportFromAttachments(attachments)
	category := ""
	if report != nil {
		category = report.Category
	}
	s.setReportScan(msg, category)
	return report, nil
}

func (s *Store) setReportScan(msg *Message, category string) {
	if msg == nil {
		return
	}
	category = cleanReportCategory(category)
	changed := false
	s.mu.Lock()
	target := msg
	if current := s.index.Messages[msg.Key]; current != nil {
		target = current
	}
	if target.ReportCategory != category || !target.ReportChecked {
		target.ReportCategory = category
		target.ReportChecked = true
		changed = true
	}
	if target != msg {
		msg.ReportCategory = target.ReportCategory
		msg.ReportChecked = target.ReportChecked
	}
	s.mu.Unlock()
	if changed {
		s.MarkDirty()
	}
}

func cleanReportCategory(category string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == aggregateReportCategory {
		return category
	}
	return ""
}

func reportCategoryFromRaw(raw []byte) (string, bool) {
	report, ok := aggregateReportFromRaw(raw)
	if !ok {
		return "", false
	}
	if report == nil {
		return "", true
	}
	return cleanReportCategory(report.Category), true
}

func aggregateReportFromRaw(raw []byte) (*AggregateReport, bool) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	attachments := []Attachment{}
	if err := extractAttachments(msg.Header, msg.Body, &attachments); err != nil {
		return nil, false
	}
	return aggregateReportFromAttachments(attachments), true
}

func aggregateReportFromAttachments(attachments []Attachment) *AggregateReport {
	for _, attachment := range attachments {
		for _, payload := range reportPayloads(attachment) {
			if report := parseAggregateReportPayload(payload.name, payload.contentType, payload.data); report != nil {
				return report
			}
		}
	}
	return nil
}

type reportPayload struct {
	name        string
	contentType string
	data        []byte
}

func reportPayloads(attachment Attachment) []reportPayload {
	base := reportPayload{name: attachment.Filename, contentType: attachment.ContentType, data: attachment.Data}
	out := []reportPayload{base}
	if data, ok := gunzipReportData(attachment.Data); ok {
		out = append(out, reportPayload{name: trimReportExt(attachment.Filename, ".gz"), contentType: attachment.ContentType, data: data})
	}
	if zippedReports(attachment.Filename, attachment.ContentType, attachment.Data) {
		reader, err := zip.NewReader(bytes.NewReader(attachment.Data), int64(len(attachment.Data)))
		if err == nil {
			for _, file := range reader.File {
				if file.FileInfo().IsDir() || file.UncompressedSize64 > aggregateReportMaxBytes {
					continue
				}
				data, err := readZipReportFile(file)
				if err == nil {
					out = append(out, reportPayload{name: file.Name, contentType: attachment.ContentType, data: data})
				}
			}
		}
	}
	return out
}

func gunzipReportData(data []byte) ([]byte, bool) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	out, err := readLimitedReport(reader)
	if err != nil {
		return nil, false
	}
	return out, true
}

func zippedReports(name, contentType string, data []byte) bool {
	ext := strings.ToLower(filepath.Ext(name))
	contentType = strings.ToLower(contentType)
	return ext == ".zip" || strings.Contains(contentType, "zip") || bytes.HasPrefix(data, []byte("PK\x03\x04"))
}

func readZipReportFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return readLimitedReport(reader)
}

func readLimitedReport(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, aggregateReportMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > aggregateReportMaxBytes {
		return nil, fmt.Errorf("report attachment too large")
	}
	return data, nil
}

func trimReportExt(name, ext string) string {
	if strings.EqualFold(filepath.Ext(name), ext) {
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return name
}

func parseAggregateReportPayload(name, contentType string, data []byte) *AggregateReport {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(name))
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if ext == ".xml" || strings.Contains(contentType, "xml") || bytes.HasPrefix(trimmed, []byte("<")) {
		if report := parseDMARCReport(trimmed); report != nil {
			return report
		}
	}
	if ext == ".json" || strings.Contains(contentType, "json") || bytes.HasPrefix(trimmed, []byte("{")) {
		if report := parseTLSReport(trimmed); report != nil {
			return report
		}
	}
	if report := parseDMARCReport(trimmed); report != nil {
		return report
	}
	return parseTLSReport(trimmed)
}

type dmarcFeedback struct {
	XMLName         xml.Name             `xml:"feedback"`
	ReportMetadata  dmarcReportMetadata  `xml:"report_metadata"`
	PolicyPublished dmarcPolicyPublished `xml:"policy_published"`
	Records         []dmarcRecord        `xml:"record"`
}

type dmarcReportMetadata struct {
	OrgName   string         `xml:"org_name"`
	Email     string         `xml:"email"`
	ReportID  string         `xml:"report_id"`
	DateRange dmarcDateRange `xml:"date_range"`
}

type dmarcDateRange struct {
	Begin string `xml:"begin"`
	End   string `xml:"end"`
}

type dmarcPolicyPublished struct {
	Domain string `xml:"domain"`
	ADKIM  string `xml:"adkim"`
	ASPF   string `xml:"aspf"`
	P      string `xml:"p"`
	SP     string `xml:"sp"`
	Pct    string `xml:"pct"`
}

type dmarcRecord struct {
	Row         dmarcRow         `xml:"row"`
	Identifiers dmarcIdentifiers `xml:"identifiers"`
	AuthResults dmarcAuthResults `xml:"auth_results"`
}

type dmarcRow struct {
	SourceIP        string               `xml:"source_ip"`
	Count           int                  `xml:"count"`
	PolicyEvaluated dmarcPolicyEvaluated `xml:"policy_evaluated"`
}

type dmarcPolicyEvaluated struct {
	Disposition string        `xml:"disposition"`
	DKIM        string        `xml:"dkim"`
	SPF         string        `xml:"spf"`
	Reasons     []dmarcReason `xml:"reason"`
}

type dmarcReason struct {
	Type    string `xml:"type"`
	Comment string `xml:"comment"`
}

type dmarcIdentifiers struct {
	HeaderFrom   string `xml:"header_from"`
	EnvelopeFrom string `xml:"envelope_from"`
}

type dmarcAuthResults struct {
	DKIM []dmarcAuthResult `xml:"dkim"`
	SPF  []dmarcAuthResult `xml:"spf"`
}

type dmarcAuthResult struct {
	Domain   string `xml:"domain"`
	Result   string `xml:"result"`
	Selector string `xml:"selector"`
	Scope    string `xml:"scope"`
}

func parseDMARCReport(data []byte) *AggregateReport {
	var feedback dmarcFeedback
	if err := xml.Unmarshal(data, &feedback); err != nil {
		return nil
	}
	if feedback.XMLName.Local != "feedback" || (feedback.ReportMetadata.ReportID == "" && feedback.PolicyPublished.Domain == "" && len(feedback.Records) == 0) {
		return nil
	}
	report := &AggregateReport{
		Category:     aggregateReportCategory,
		Kind:         "DMARC",
		Organization: firstNonEmptyString(cleanReportText(feedback.ReportMetadata.OrgName), cleanReportText(feedback.ReportMetadata.Email)),
		Domain:       cleanReportText(feedback.PolicyPublished.Domain),
		ReportID:     cleanReportText(feedback.ReportMetadata.ReportID),
		DateRange:    reportDateRange(feedback.ReportMetadata.DateRange.Begin, feedback.ReportMetadata.DateRange.End),
	}
	policy := firstNonEmptyString(cleanReportText(feedback.PolicyPublished.P), cleanReportText(feedback.PolicyPublished.SP))
	for _, record := range feedback.Records {
		row := AggregateReportRow{
			Source: cleanReportText(record.Row.SourceIP),
			Count:  strconv.Itoa(record.Row.Count),
			Policy: cleanReportText(firstNonEmptyString(record.Row.PolicyEvaluated.Disposition, policy)),
			DKIM:   dmarcAuthSummary(record.AuthResults.DKIM, record.Row.PolicyEvaluated.DKIM),
			SPF:    dmarcAuthSummary(record.AuthResults.SPF, record.Row.PolicyEvaluated.SPF),
			Result: cleanReportText(record.Identifiers.HeaderFrom),
			Detail: dmarcReasonSummary(record.Row.PolicyEvaluated.Reasons),
		}
		if row.Result == "" {
			row.Result = cleanReportText(record.Identifiers.EnvelopeFrom)
		}
		report.Rows = append(report.Rows, row)
	}
	return report
}

func dmarcAuthSummary(results []dmarcAuthResult, fallback string) string {
	items := []string{}
	for _, result := range results {
		domain := cleanReportText(result.Domain)
		status := cleanReportText(result.Result)
		if domain == "" && status == "" {
			continue
		}
		if domain == "" {
			items = append(items, status)
		} else if status == "" {
			items = append(items, domain)
		} else {
			items = append(items, domain+":"+status)
		}
	}
	if len(items) > 0 {
		return strings.Join(items, ", ")
	}
	return cleanReportText(fallback)
}

func dmarcReasonSummary(reasons []dmarcReason) string {
	items := []string{}
	for _, reason := range reasons {
		typ := cleanReportText(reason.Type)
		comment := cleanReportText(reason.Comment)
		if typ == "" && comment == "" {
			continue
		}
		if comment == "" {
			items = append(items, typ)
		} else if typ == "" {
			items = append(items, comment)
		} else {
			items = append(items, typ+": "+comment)
		}
	}
	return strings.Join(items, "; ")
}

type tlsAggregateReport struct {
	OrganizationName string       `json:"organization-name"`
	DateRange        tlsDateRange `json:"date-range"`
	ContactInfo      string       `json:"contact-info"`
	ReportID         string       `json:"report-id"`
	Policies         []tlsPolicy  `json:"policies"`
}

type tlsDateRange struct {
	Start string `json:"start-datetime"`
	End   string `json:"end-datetime"`
}

type tlsPolicy struct {
	Policy         tlsPolicyInfo      `json:"policy"`
	Summary        tlsPolicySummary   `json:"summary"`
	FailureDetails []tlsFailureDetail `json:"failure-details"`
}

type tlsPolicyInfo struct {
	PolicyType   string   `json:"policy-type"`
	PolicyDomain string   `json:"policy-domain"`
	MXHost       []string `json:"mx-host"`
	PolicyString []string `json:"policy-string"`
}

type tlsPolicySummary struct {
	Successful int `json:"total-successful-session-count"`
	Failure    int `json:"total-failure-session-count"`
}

type tlsFailureDetail struct {
	ResultType            string `json:"result-type"`
	SendingMTAIP          string `json:"sending-mta-ip"`
	ReceivingMXHostname   string `json:"receiving-mx-hostname"`
	ReceivingMXHelo       string `json:"receiving-mx-helo"`
	FailedSessionCount    int    `json:"failed-session-count"`
	AdditionalInformation string `json:"additional-information"`
	FailureReasonCode     string `json:"failure-reason-code"`
}

func parseTLSReport(data []byte) *AggregateReport {
	var input tlsAggregateReport
	if err := json.Unmarshal(data, &input); err != nil {
		return nil
	}
	if len(input.Policies) == 0 || (input.ReportID == "" && input.OrganizationName == "") {
		return nil
	}
	report := &AggregateReport{
		Category:     aggregateReportCategory,
		Kind:         "TLSRPT",
		Organization: cleanReportText(input.OrganizationName),
		ReportID:     cleanReportText(input.ReportID),
		DateRange:    reportDateRange(input.DateRange.Start, input.DateRange.End),
	}
	for _, policy := range input.Policies {
		domain := cleanReportText(policy.Policy.PolicyDomain)
		if report.Domain == "" {
			report.Domain = domain
		}
		source := firstNonEmptyString(strings.Join(policy.Policy.MXHost, ", "), domain)
		result := "ok"
		if policy.Summary.Failure > 0 {
			result = "failures"
		}
		report.Rows = append(report.Rows, AggregateReportRow{
			Source: cleanReportText(source),
			Count:  fmt.Sprintf("%d/%d", policy.Summary.Successful, policy.Summary.Failure),
			Policy: cleanReportText(policy.Policy.PolicyType),
			Result: result,
			Detail: "success/failure",
		})
		for _, detail := range policy.FailureDetails {
			report.Rows = append(report.Rows, AggregateReportRow{
				Source: cleanReportText(firstNonEmptyString(detail.SendingMTAIP, detail.ReceivingMXHostname, detail.ReceivingMXHelo, source)),
				Count:  strconv.Itoa(detail.FailedSessionCount),
				Policy: cleanReportText(policy.Policy.PolicyType),
				Result: cleanReportText(detail.ResultType),
				Detail: tlsFailureSummary(detail),
			})
		}
	}
	return report
}

func tlsFailureSummary(detail tlsFailureDetail) string {
	items := []string{}
	for _, value := range []string{detail.ReceivingMXHostname, detail.FailureReasonCode, detail.AdditionalInformation} {
		value = cleanReportText(value)
		if value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, "; ")
}

func reportDateRange(start, end string) string {
	start = reportTime(start)
	end = reportTime(end)
	if start == "" {
		return end
	}
	if end == "" || end == start {
		return start
	}
	return start + " - " + end
}

func reportTime(value string) string {
	value = cleanReportText(value)
	if value == "" {
		return ""
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC().Format("2006-01-02")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return value
}

func cleanReportText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
