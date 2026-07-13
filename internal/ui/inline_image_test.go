package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestInlineImageRasterUsesHalfBlocksAndTransparency(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(0, 8, color.NRGBA{B: 255, A: 255})
	source.SetNRGBA(8, 0, color.NRGBA{G: 255, A: 127})
	source.SetNRGBA(8, 8, color.NRGBA{G: 255, A: 128})
	value := &previewImage{source: source, rows: map[int][][]pixelCell{}}
	rows := value.rasterRows(48)
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if !rows[0][0].topSet || !rows[0][0].bottomSet || rows[0][0].top.R != 255 || rows[0][0].bottom.B != 255 {
		t.Fatalf("first cell = %#v", rows[0][0])
	}
	if rows[0][1].topSet || !rows[0][1].bottomSet {
		t.Fatalf("alpha threshold cell = %#v", rows[0][1])
	}
	output := captureStdout(t, func() { printPixelLine(0, 0, 2, rows[0]) })
	for _, want := range []string{"38;2;255;0;0;48;2;0;0;255m▀", "38;2;0;255;0;49m▄"} {
		if !strings.Contains(output, want) {
			t.Fatalf("pixel output missing %q in %q", want, output)
		}
	}
}

func TestInlineImageRasterSizingAndNoUpscale(t *testing.T) {
	small := &previewImage{source: image.NewNRGBA(image.Rect(0, 0, 16, 24)), rows: map[int][][]pixelCell{}}
	rows := small.rasterRows(80)
	if len(rows) != 2 || len(rows[0]) != 2 {
		t.Fatalf("small raster size = %dx%d", len(rows[0]), len(rows))
	}
	large := &previewImage{source: image.NewNRGBA(image.Rect(0, 0, 100, 100)), rows: map[int][][]pixelCell{}}
	rows = large.rasterRows(100)
	if len(rows) != 6 || len(rows[0]) != 12 {
		t.Fatalf("large raster size = %dx%d", len(rows[0]), len(rows))
	}
	narrow := large.rasterRows(10)
	if len(narrow) != 5 || len(narrow[0]) != 10 {
		t.Fatalf("narrow raster size = %dx%d", len(narrow[0]), len(narrow))
	}
	capped := &previewImage{source: image.NewNRGBA(image.Rect(0, 0, 800, 800)), rows: map[int][][]pixelCell{}}
	rows = capped.rasterRows(100)
	if len(rows) != inlineImageMaxTerminalRows || len(rows[0]) != inlineImageMaxColumns {
		t.Fatalf("capped raster size = %dx%d", len(rows[0]), len(rows))
	}
}

func TestInlineImageUsesEightPixelsPerColumnUpperBound(t *testing.T) {
	value := &previewImage{source: image.NewNRGBA(image.Rect(0, 0, 16, 16)), rows: map[int][][]pixelCell{}}
	rows := value.rasterRows(80)
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("raster size = %dx%d, want 2x1", len(rows[0]), len(rows))
	}
	tiny := &previewImage{source: image.NewNRGBA(image.Rect(0, 0, 7, 7)), rows: map[int][][]pixelCell{}}
	rows = tiny.rasterRows(80)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("tiny raster size = %dx%d, want 1x1", len(rows[0]), len(rows))
	}
}

func TestPreparePreviewPartsDecodesPNGAndFallsBack(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 12, G: 34, B: 56, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	parts := preparePreviewParts([]store.BodyPart{
		{Text: "before"},
		{Image: &store.InlineImage{Filename: "valid.png", ContentType: "image/png", Data: encoded.Bytes()}},
		{Image: &store.InlineImage{Filename: "broken.png", Alt: "broken", ContentType: "image/png", Data: []byte("bad")}},
	})
	if len(parts) != 3 || parts[0].text != "before" || parts[1].image == nil || parts[2].text != "[image: broken]" {
		t.Fatalf("parts = %#v", parts)
	}
	rows := previewLinesWithParts(&store.Message{}, parts, 40)
	imageRows := 0
	for _, row := range rows {
		if row.pixels != nil {
			imageRows++
			if text := previewPlainText(row); text != "" {
				t.Fatalf("pixel row plain text = %q", text)
			}
			if links := previewLinks(row); len(links) != 0 {
				t.Fatalf("pixel row links = %#v", links)
			}
		}
	}
	if imageRows != 1 {
		t.Fatalf("image rows = %d in %#v", imageRows, rows)
	}
}

func TestInlineImageCountLimitFallsBack(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	input := make([]store.BodyPart, inlineImageMaxCount+1)
	for i := range input {
		input[i].Image = &store.InlineImage{Filename: "pixel.png", Data: encoded.Bytes()}
	}
	parts := preparePreviewParts(input)
	images, fallbacks := 0, 0
	for _, part := range parts {
		if part.image != nil {
			images++
		}
		if part.text != "" {
			fallbacks++
		}
	}
	if images != inlineImageMaxCount || fallbacks != 1 {
		t.Fatalf("images = %d, fallbacks = %d", images, fallbacks)
	}
}

func TestScrollBodyCountsInlineImageRows(t *testing.T) {
	value := &previewImage{source: image.NewNRGBA(image.Rect(0, 0, 4, 8)), rows: map[int][][]pixelCell{}}
	app := &App{
		preview:      &store.Message{},
		previewParts: []previewPart{{text: "before"}, {image: value}, {text: "after"}},
		bodyArea:     area{h: 3, w: 20},
	}
	lines := app.currentPreviewLines(app.bodyArea.w)
	app.scrollBody(100)
	if app.bodyScroll != len(lines)-app.bodyArea.h {
		t.Fatalf("bodyScroll = %d, want %d", app.bodyScroll, len(lines)-app.bodyArea.h)
	}
}

func TestImageAttachmentHitTargetUsesRenderedBounds(t *testing.T) {
	value := &previewImage{
		source:     image.NewNRGBA(image.Rect(0, 0, 16, 16)),
		attachment: store.Attachment{Filename: "logo.png", ContentType: "image/png", Data: []byte("image")},
		rows:       map[int][][]pixelCell{},
	}
	app := &App{
		preview:      &store.Message{},
		previewParts: []previewPart{{text: "before"}, {image: value}, {text: "after"}},
		bodyArea:     area{y: 10, x: 5, h: 20, w: 40},
	}
	lines := app.currentPreviewLines(app.bodyArea.w)
	imageLine := -1
	imageWidth := 0
	for i, line := range lines {
		if line.image != nil {
			imageLine = i
			imageWidth = len(line.pixels)
			break
		}
	}
	if imageLine < 0 || imageWidth != 2 {
		t.Fatalf("image line = %d, width = %d", imageLine, imageWidth)
	}
	attachment, ok := app.imageAttachmentAtBodyPoint(app.bodyArea.x, app.bodyArea.y+imageLine)
	if !ok || attachment.Filename != "logo.png" || string(attachment.Data) != "image" {
		t.Fatalf("attachment = %#v, ok = %v", attachment, ok)
	}
	if _, ok := app.imageAttachmentAtBodyPoint(app.bodyArea.x+imageWidth, app.bodyArea.y+imageLine); ok {
		t.Fatal("blank padding beside image was clickable")
	}
	if _, ok := app.imageAttachmentAtBodyPoint(app.bodyArea.x, app.bodyArea.y+imageLine-1); ok {
		t.Fatal("text row above image was clickable")
	}
}
