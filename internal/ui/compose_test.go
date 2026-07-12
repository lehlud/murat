package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"
)

func TestComposePGPLineHidesOptionsOutsideMenu(t *testing.T) {
	line := composePGPLine(protocol.Draft{}, pgp.Availability{Sign: true, AttachPublicKey: true}, false)
	if strings.Contains(line, "sign=") || strings.Contains(line, "pubkey=") || strings.Contains(line, "encrypt=") || strings.Contains(line, "self=") {
		t.Fatalf("line shows menu options outside menu: %q", line)
	}
}

func TestComposePGPMenuHidesUnavailableOptions(t *testing.T) {
	line := composePGPLine(protocol.Draft{}, pgp.Availability{Sign: true, AttachPublicKey: true}, true)
	if !strings.Contains(line, "sign=") || !strings.Contains(line, "pubkey=") {
		t.Fatalf("line missing available options: %q", line)
	}
	if strings.Contains(line, "encrypt=") || strings.Contains(line, "self=") {
		t.Fatalf("line shows unavailable options: %q", line)
	}
}

func TestToggleSelfEncryptEnablesEncrypt(t *testing.T) {
	draft := protocol.Draft{}
	togglePGP(&draft, "self-encrypt")
	options := pgpSet(draft.PGP)
	if !options["encrypt"] || !options["self-encrypt"] {
		t.Fatalf("PGP options = %q", draft.PGP)
	}
	togglePGP(&draft, "encrypt")
	options = pgpSet(draft.PGP)
	if options["encrypt"] || options["self-encrypt"] {
		t.Fatalf("PGP options after disabling encrypt = %q", draft.PGP)
	}
}

func TestSecretBufferHelpers(t *testing.T) {
	value, chars := removeLastSecretRune([]byte("abc"), 3)
	if string(value) != "ab" || chars != 2 {
		t.Fatalf("removeLastSecretRune = %q, %d", value, chars)
	}
	clearSecretBytes(value)
	for _, b := range value {
		if b != 0 {
			t.Fatalf("secret byte not cleared: %#v", value)
		}
	}
}

func TestMarkdownLinks(t *testing.T) {
	links := markdownLinks("Read [the docs](https://example.com) now")
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].start != 5 || links[0].end != 13 || links[0].url != "https://example.com" {
		t.Fatalf("link = %#v", links[0])
	}
}

func TestNormalizePreviewTextCompactsForwardedHeaderNoise(t *testing.T) {
	input := strings.Join([]string{
		"Von: Ingrid",
		"Enzensberger | Bauinnung Nürnberg <enzensberger@bauinnung-nuernberg.de>",
		"",
		"Gesendet: Freitag, 10. Juli 2026 07:50",
		"",
		"An: info@bauinnung-nuernberg.de",
		"",
		"Betreff: Erinnerung! // WG: Einladung zum Workshop",
		"",
		" ",
		"Es sind noch Plätze frei!",
		" ",
		"Mit freundlichen Grüßen",
	}, "\n")
	got := normalizePreviewText(input)
	want := strings.Join([]string{
		"Von: Ingrid Enzensberger | Bauinnung Nürnberg <enzensberger@bauinnung-nuernberg.de>",
		"Gesendet: Freitag, 10. Juli 2026 07:50",
		"An: info@bauinnung-nuernberg.de",
		"Betreff: Erinnerung! // WG: Einladung zum Workshop",
		"",
		"Es sind noch Plätze frei!",
		"",
		"Mit freundlichen Grüßen",
	}, "\n")
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestTelephoneMarkdownLinks(t *testing.T) {
	line := "Tel. [0911 / 661551](tel:+49911661551), Mobil: [0175 / 613 6616](tel:+491756136616), Fax [0911 / 666713](tel:+49911666713)"
	plain := richPlainText(line)
	if strings.Contains(plain, "tel:") {
		t.Fatalf("plain text exposed tel URL: %q", plain)
	}
	links := markdownLinks(line)
	if len(links) != 3 {
		t.Fatalf("links = %#v", links)
	}
	wantURLs := []string{"tel:+49911661551", "tel:+491756136616", "tel:+49911666713"}
	wantLabels := []string{"0911 / 661551", "0175 / 613 6616", "0911 / 666713"}
	for i := range wantURLs {
		if links[i].url != wantURLs[i] {
			t.Fatalf("link[%d].url = %q, want %q", i, links[i].url, wantURLs[i])
		}
		labelStart := strings.Index(plain, wantLabels[i])
		if labelStart < 0 {
			t.Fatalf("plain text missing label %q in %q", wantLabels[i], plain)
		}
		labelCol := displayLen(plain[:labelStart])
		if links[i].start != labelCol || links[i].end != labelCol+displayLen(wantLabels[i]) {
			t.Fatalf("link[%d] range = %#v, want %d-%d", i, links[i], labelCol, labelCol+displayLen(wantLabels[i]))
		}
	}
}

func TestBareURLLinks(t *testing.T) {
	text := "see https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwesen-umsetzen-1279612 now"
	links := markdownLinks(text)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	url := "https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwesen-umsetzen-1279612"
	if links[0].start != 4 || links[0].url != url {
		t.Fatalf("link = %#v", links[0])
	}
}

func TestWrapUnwrapsBrokenBareURL(t *testing.T) {
	url := "https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwesen-umsetzen-1279612"
	broken := "https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwe\nsen-umsetzen-1279612"
	lines := wrap(joinWrappedBareURLs(broken), 40)
	if strings.Contains(strings.Join(lines, "\n"), "bauwe\nsen") {
		t.Fatalf("url remained broken: %#v", lines)
	}
	for _, line := range lines {
		links := markdownLinks(line)
		if len(links) == 0 {
			continue
		}
		if links[0].url != url {
			t.Fatalf("url = %q, want %q", links[0].url, url)
		}
		return
	}
	t.Fatalf("wrapped lines contain no resolvable link: %#v", lines)
}

func TestPreviewRepairsControlCharWrappedAngleURL(t *testing.T) {
	url := "https://scanmail.trustwave.com/?c079&d=gcKe4AqXM64IDhVCiyKglgvUNrQbGo5n1xr1CmZ-mw&u=https%3a%2f%2fergo-frontend%2eonlinetermine%2ecom%2f000160572%2fstart%3f"
	input := "[Tageskalender mit einfarbiger Füllung]  Termin buchen<https://scanmail.trustwave.com/?c\x17079&d=gcKe4AqXM64IDhVCiyKglgvUNrQbGo5n1xr1CmZ-mw&u=https%3a%2f%2\nfergo-frontend%2eonlinetermine%2ecom%2f000160572%2fstart%3f>     |     [Monitor mit einfarbiger Füllung]  Kundenportal"
	rows := previewLines(&store.Message{}, input, 360)
	var body previewLine
	for _, row := range rows {
		if strings.Contains(row.text, "scanmail.trustwave.com") {
			body = row
			break
		}
	}
	if body.text == "" {
		t.Fatalf("scanmail line missing from %#v", rows)
	}
	if strings.Contains(body.text, "\x17") || strings.Contains(body.text, "%2\nfergo") {
		t.Fatalf("line was not normalized: %q", body.text)
	}
	links := previewLinks(body)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].url != url {
		t.Fatalf("url = %q, want %q", links[0].url, url)
	}
	plain := previewPlainText(body)
	if !strings.Contains(plain, "Termin buchen") {
		t.Fatalf("plain text missing link label: %q", plain)
	}
	if strings.Contains(plain, "scanmail.trustwave.com") || strings.Contains(plain, "<https://") {
		t.Fatalf("plain text exposed outlook URL: %q", plain)
	}
	labelStart := strings.Index(plain, "Termin buchen")
	labelCol := displayLen(plain[:labelStart])
	if links[0].start != labelCol || links[0].end != labelCol+displayLen("Termin buchen") {
		t.Fatalf("link range = %#v, want %d-%d", links[0], labelCol, labelCol+displayLen("Termin buchen"))
	}
	got := captureStdout(t, func() {
		printSelectedPreviewLine(0, 0, 360, body, [2]int{0, displayLen(plain)})
	})
	if strings.Contains(got, "\x17") || strings.Contains(got, "scanmail.trustwave.com") {
		t.Fatalf("selected output exposed hidden URL/control char: %q", got)
	}
}

func TestOutlookLinkDoesNotConsumeQuotedMailHeader(t *testing.T) {
	line := previewLine{text: "On 2026-07-10T06:18:59Z, Torsten.Boehner@ergo.de <mailto:Torsten.Boehner@ergo.de> wrote:"}
	plain := previewPlainText(line)
	if plain != line.text {
		t.Fatalf("plain text = %q, want %q", plain, line.text)
	}
	links := previewLinks(line)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	wantStart := strings.Index(plain, "mailto:")
	if wantStart < 0 {
		t.Fatal("test line missing mailto")
	}
	wantCol := displayLen(plain[:wantStart])
	if links[0].start != wantCol || links[0].end <= links[0].start || links[0].url != "mailto:Torsten.Boehner@ergo.de" {
		t.Fatalf("link = %#v, want only visible mailto at %d", links[0], wantCol)
	}
}

func TestOutlookLinkDoesNotConsumeNestedQuotedMailHeader(t *testing.T) {
	line := previewLine{text: "On 2026-07-10T06:18:59Z, Torsten.Boehner@ergo.de <Torsten.Boehner@ergo.de<mailto:Torsten.Boehner@ergo.de>> wrote:"}
	plain := previewPlainText(line)
	if plain != line.text {
		t.Fatalf("plain text = %q, want %q", plain, line.text)
	}
	if links := previewLinks(line); len(links) != 1 || links[0].url != "mailto:Torsten.Boehner@ergo.de" {
		t.Fatalf("links = %#v", links)
	}
}

func TestOutlookCompactAddressAndDomainLinks(t *testing.T) {
	line := previewLine{text: "Tel 0921 786630-55, torsten.boehner@ergo.de<mailto:torsten.boehner@ergo.de> agentur-boehner@ergo.de<mailto:agentur-boehner@ergo.de>, www.agentur-boehner.de<http://www.agentur-boehner.de>"}
	plain := previewPlainText(line)
	for _, hidden := range []string{"<mailto:", "<http://"} {
		if strings.Contains(plain, hidden) {
			t.Fatalf("plain text exposed outlook URL %q in %q", hidden, plain)
		}
	}
	links := previewLinks(line)
	if len(links) != 3 {
		t.Fatalf("links = %#v", links)
	}
	wants := []struct {
		label string
		url   string
	}{
		{"torsten.boehner@ergo.de", "mailto:torsten.boehner@ergo.de"},
		{"agentur-boehner@ergo.de", "mailto:agentur-boehner@ergo.de"},
		{"www.agentur-boehner.de", "http://www.agentur-boehner.de"},
	}
	for i, want := range wants {
		if links[i].url != want.url {
			t.Fatalf("link[%d].url = %q, want %q", i, links[i].url, want.url)
		}
		labelStart := strings.Index(plain, want.label)
		if labelStart < 0 {
			t.Fatalf("plain text missing label %q in %q", want.label, plain)
		}
		labelCol := displayLen(plain[:labelStart])
		if links[i].start != labelCol || links[i].end != labelCol+displayLen(want.label) {
			t.Fatalf("link[%d] range = %#v, want %d-%d", i, links[i], labelCol, labelCol+displayLen(want.label))
		}
	}
}

func TestPreviewDisplaysSpacedMultilineOutlookLinkAsLabel(t *testing.T) {
	url := "https://scanmail.trustwave.com/?c079&d=gcKe4AqXM64IDhVCiyKglgvUNrQbGo5n1xisXmN_mw&u=https%3a%2f%2fkunde-s%2eergo%2ede%2fmeineversicherungen%2flz%2fstart%2easpx%3fvu%3dergo"
	input := "Kundenportal    <\n" + url + "\n>"
	rows := previewLines(&store.Message{}, input, 240)
	var body previewLine
	for _, row := range rows {
		if strings.Contains(row.text, "Kundenportal") {
			body = row
			break
		}
	}
	if body.text == "" {
		t.Fatalf("kundenportal line missing from %#v", rows)
	}
	plain := previewPlainText(body)
	if plain != "Kundenportal" {
		t.Fatalf("plain text = %q, want link label only", plain)
	}
	links := previewLinks(body)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].url != url || links[0].start != 0 || links[0].end != displayLen("Kundenportal") {
		t.Fatalf("link = %#v", links[0])
	}
	got := captureStdout(t, func() {
		printSelectedPreviewLine(0, 0, 240, body, [2]int{0, displayLen(plain)})
	})
	if strings.Contains(got, "scanmail.trustwave.com") || strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("selected output exposed hidden URL: %q", got)
	}
}

func TestWideEmojiLinkLabelKeepsTrailingCharacter(t *testing.T) {
	label := "👉 Einsätze schwarz auf weiß belegen"
	line := previewLine{text: "[" + label + "](https://example.com)"}
	plain := previewPlainText(line)
	if plain != label {
		t.Fatalf("plain text = %q, want %q", plain, label)
	}
	if displayLen(label) != len([]rune(label))+1 {
		t.Fatalf("emoji label displayLen = %d, rune len = %d", displayLen(label), len([]rune(label)))
	}
	links := previewLinks(line)
	if len(links) != 1 || links[0].end != displayLen(label) {
		t.Fatalf("links = %#v, want end %d", links, displayLen(label))
	}
	got := captureStdout(t, func() {
		printPreviewLine(0, 0, displayLen(label)+1, line)
	})
	if !strings.Contains(got, "belegen") {
		t.Fatalf("rendered link label lost trailing text: %q", got)
	}
}

func TestSliceRunesUsesDisplayColumns(t *testing.T) {
	value := "👉 Einsätze"
	if got := sliceRunes(value, 0, 2); got != "👉" {
		t.Fatalf("slice wide rune = %q", got)
	}
	if got := sliceRunes(value, 0, 1); got != "" {
		t.Fatalf("partial wide rune slice = %q", got)
	}
	if got := sliceRunes(value, 2, displayLen(value)); got != " Einsätze" {
		t.Fatalf("slice after wide rune = %q", got)
	}
}

func TestPreviewDisplaysMultilineMarkdownLinkLabel(t *testing.T) {
	url := "https://geocapture.org/nl/bc8d62db07fa71c4e33683b38cc3e010-36911185?url=https%3A%2F%2Fwww.geocapture.de%2Ffahrzeugortung-gps%3Futm_source%3Dnl%26utm_medium%3Demail&ref#63"
	input := "[👉\nEinsätze schwarz auf weiß belegen ](\n" + url + "\n)"
	rows := previewLines(&store.Message{}, input, 240)
	var body previewLine
	for _, row := range rows {
		if strings.Contains(row.text, "Einsätze") {
			body = row
			break
		}
	}
	if body.text == "" {
		t.Fatalf("labeled markdown line missing from %#v", rows)
	}
	plain := previewPlainText(body)
	want := "👉 Einsätze schwarz auf weiß belegen"
	if plain != want {
		t.Fatalf("plain text = %q, want %q", plain, want)
	}
	if strings.Contains(plain, "geocapture.org") || strings.Contains(plain, "https://") {
		t.Fatalf("plain text exposed URL: %q", plain)
	}
	links := previewLinks(body)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].url != url || links[0].start != 0 || links[0].end != displayLen(want) {
		t.Fatalf("link = %#v", links[0])
	}
}

func TestPreviewHidesEmptyMultilineMarkdownLink(t *testing.T) {
	url := "https://geocapture.org/nl/bc8d62db07fa71c4e33683b38cc3e010-36911185?url=https%3A%2F%2Fwww.geocapture.de%2Ffahrzeugortung-gps%3Futm_source%3Dnl%26utm_medium%3Demail&ref#63"
	rows := previewLines(&store.Message{}, "[](\n"+url+"\n)", 240)
	for _, row := range rows {
		plain := previewPlainText(row)
		if strings.Contains(plain, "geocapture.org") || strings.Contains(plain, "https://") {
			t.Fatalf("empty markdown link exposed URL in %#v", rows)
		}
		if links := previewLinks(row); len(links) != 0 {
			t.Fatalf("empty markdown link created visible click range: %#v", links)
		}
	}
}

func TestRichPlainTextShowsLinkLabelOnly(t *testing.T) {
	got := richPlainText("Read [the docs](https://example.com) now")
	want := "Read the docs now"
	if got != want {
		t.Fatalf("richPlainText() = %q, want %q", got, want)
	}
}

func TestPreviewWrapCarriesRichFormattingAcrossSourceNewline(t *testing.T) {
	lines := wrapPreviewContent("*Wie Sie wissen, können über das Internet versandte Emails leicht unter fremden Namen erstellt werden. Aus diesem\nGrund bitten wir um Verständnis dafür.*", 160)
	if len(lines) < 2 {
		t.Fatalf("wrapped lines = %#v", lines)
	}
	if !lines[1].rich.italic {
		t.Fatalf("second source line did not carry italic state: %#v", lines)
	}
	got := captureStdout(t, func() {
		printPreviewLine(1, 0, 160, lines[1])
	})
	if !strings.Contains(got, styleItalic) {
		t.Fatalf("continued source line missing italic style: %q", got)
	}
}

func TestPreviewWrapCarriesRichFormatting(t *testing.T) {
	lines := wrapPreviewContent("**bold text continues across wrap** and *italic text continues too*", 12)
	if len(lines) < 4 {
		t.Fatalf("wrapped lines = %#v", lines)
	}
	if !lines[1].rich.bold {
		t.Fatalf("second line did not carry bold state: %#v", lines)
	}
	if !lines[4].rich.italic {
		t.Fatalf("italic line did not carry italic state: %#v", lines)
	}
	boldOut := captureStdout(t, func() {
		printPreviewLine(0, 0, 80, lines[1])
	})
	if !strings.Contains(boldOut, styleBold) {
		t.Fatalf("wrapped bold line missing style: %q", boldOut)
	}
	italicOut := captureStdout(t, func() {
		printPreviewLine(0, 0, 80, lines[4])
	})
	if !strings.Contains(italicOut, styleItalic) {
		t.Fatalf("wrapped italic line missing style: %q", italicOut)
	}
}

func TestRichLineDoesNotBlankBeforeDrawing(t *testing.T) {
	got := captureStdout(t, func() {
		printRichLine(0, 0, 20, "**bold**", richState{})
	})
	if strings.Contains(got, "\x1b[1;1H                    ") {
		t.Fatalf("rich line blanked row before drawing: %q", got)
	}
	if !strings.Contains(got, styleBold) {
		t.Fatalf("rich line missing bold style: %q", got)
	}
}

func TestSelectedPreviewLinePreservesRichFormatting(t *testing.T) {
	line := previewLine{text: "Read **bold** *italic* __under__ [docs](https://example.com)"}
	selected := [2]int{0, displayLen(previewPlainText(line))}
	got := captureStdout(t, func() {
		printSelectedPreviewLine(0, 0, 120, line, selected)
	})
	for _, want := range []string{styleSelected, styleBold, styleItalic, styleUnder, styleLink} {
		if !strings.Contains(got, want) {
			t.Fatalf("selected rich line missing style %q in %q", want, got)
		}
	}
	for _, raw := range []string{"**", "__", "[docs]"} {
		if strings.Contains(got, raw) {
			t.Fatalf("selected rich line leaked markup %q in %q", raw, got)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestWrapKeepsLongMarkdownLinkResolvable(t *testing.T) {
	url := "https://manage.kmail-lists.com/subscriptions/unsubscribe?a=" + strings.Repeat("x", 96)
	lines := wrap("No longer want [Unsubscribe]("+url+") now", 20)
	for _, line := range lines {
		links := markdownLinks(line)
		if len(links) == 0 {
			continue
		}
		if links[0].url != url {
			t.Fatalf("url = %q, want %q", links[0].url, url)
		}
		if got := richPlainText(line); !strings.Contains(got, "Unsubscribe") {
			t.Fatalf("plain wrapped line = %q", got)
		}
		return
	}
	t.Fatalf("wrapped lines contain no resolvable link: %#v", lines)
}

func TestAutoScrollSelectionExtendsAtPreviewBottom(t *testing.T) {
	body := strings.Join([]string{
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
		"line 06",
		"line 07",
		"line 08",
		"line 09",
		"line 10",
	}, "\n")
	app := &App{
		preview:     &store.Message{Subject: "subject"},
		previewBody: body,
		bodyArea:    area{y: 10, x: 0, h: 3, w: 40},
		bodyScroll:  5,
	}

	app.startSelection(0, 11)
	app.selectDrag = true
	app.updateSelection(0, 12)
	if !app.autoScrollSelection() {
		t.Fatal("expected selection edge to scroll")
	}
	if app.bodyScroll != 6 {
		t.Fatalf("bodyScroll = %d, want 6", app.bodyScroll)
	}
	if app.selectEnd == nil || app.selectEnd.line != 8 {
		t.Fatalf("selectEnd = %#v, want line 8", app.selectEnd)
	}
}

func TestMessageWithinDays(t *testing.T) {
	now := time.Now().UTC()
	if !messageWithinDays(&store.Message{ReceivedAt: now.Format(time.RFC3339)}, 0) {
		t.Fatal("expected message today")
	}
	if messageWithinDays(&store.Message{ReceivedAt: now.AddDate(0, 0, -1).Format(time.RFC3339)}, 0) {
		t.Fatal("unexpected message before today")
	}
	if !messageWithinDays(&store.Message{ReceivedAt: now.AddDate(0, 0, -7).Format(time.RFC3339)}, 7) {
		t.Fatal("expected message within 7 days")
	}
	if messageWithinDays(&store.Message{ReceivedAt: now.AddDate(0, 0, -8).Format(time.RFC3339)}, 7) {
		t.Fatal("unexpected message older than 7 days")
	}
}

func TestFormatDraftPreviewShowsAttachments(t *testing.T) {
	preview := formatDraftPreview(protocol.Draft{
		From:    "alice@example.com",
		To:      "bob@example.com",
		Subject: "hi",
		Body:    "body",
		Attachments: []protocol.Attachment{{
			Filename:    "notes.txt",
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	})
	for _, want := range []string{"Attachments:", "  - notes.txt (text/plain, 5B)", "\n\nbody"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
}

func TestDraftAttachmentFromPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment, err := draftAttachmentFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != "notes.txt" || string(attachment.Data) != "hello" {
		t.Fatalf("attachment = %#v", attachment)
	}
	if !strings.HasPrefix(attachment.ContentType, "text/plain") {
		t.Fatalf("content type = %q", attachment.ContentType)
	}
}

func TestDraftAttachmentsFromDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	attachments, err := draftAttachmentsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments len = %d", len(attachments))
	}
	if attachments[0].Filename != "a.txt" || string(attachments[0].Data) != "a" {
		t.Fatalf("attachment[0] = %#v", attachments[0])
	}
	if attachments[1].Filename != "b.txt" || string(attachments[1].Data) != "b" {
		t.Fatalf("attachment[1] = %#v", attachments[1])
	}
}

func TestDraftAttachmentsFromDirectoryRequiresFiles(t *testing.T) {
	if _, err := draftAttachmentsFromDirectory(t.TempDir()); err == nil {
		t.Fatal("expected empty directory error")
	}
}
