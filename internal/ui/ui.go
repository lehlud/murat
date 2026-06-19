package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lehnert.dev/murat/internal/compose"
	"lehnert.dev/murat/internal/config"
	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"
	"lehnert.dev/murat/internal/userdirs"
)

const styleReset = "\x1b[0m"

var (
	defaultTheme  = DefaultTheme()
	styleHeader   = defaultTheme.Header
	styleLabel    = defaultTheme.Label
	styleSelected = defaultTheme.Selected
	styleUnread   = defaultTheme.Unread
	styleStatus   = defaultTheme.Status
	styleError    = defaultTheme.Error
	styleDivider  = defaultTheme.Divider
	styleTag      = defaultTheme.Tag
	styleSpam     = defaultTheme.Spam
	styleDim      = defaultTheme.Dim
	styleLink     = defaultTheme.Link
	styleBold     = "\x1b[1m"
	styleItalic   = "\x1b[3m"
	styleUnder    = "\x1b[4m"
)

type Options struct {
	PGPDefaults string
	PGPIdentity string
	Theme       Theme
	Keys        Keys
	Editor      string
	Split       string
	PageSize    int
}

type Theme struct {
	Header   string
	Label    string
	Selected string
	Unread   string
	Status   string
	Error    string
	Divider  string
	Tag      string
	Spam     string
	Dim      string
	Link     string
}

type Keys struct {
	Quit         string
	ForceQuit    string
	Sync         string
	Compose      string
	Filter       string
	Actions      string
	Open         string
	Next         string
	Prev         string
	Down         string
	Up           string
	PageDown     string
	PageUp       string
	Top          string
	Bottom       string
	CycleAccount string
}

type area struct {
	y int
	x int
	h int
	w int
}

type point struct {
	line int
	col  int
}

type App struct {
	store        *store.Store
	accounts     *store.AccountStore
	events       chan event
	listArea     area
	bodyArea     area
	selectStart  *point
	selectEnd    *point
	selectActive bool
	selectToken  int
	dirty        bool
	lastW        int
	lastH        int
	messages     []*store.Message
	accountsList []store.Account
	selected     int
	scroll       int
	bodyScroll   int
	preview      *store.Message
	previewBody  string
	previewHead  string
	headerMode   bool
	status       string
	error        bool
	filter       string
	filterQuery  string
	pendingURL   string
	account      int
	actionScope  string
	syncRunning  bool
	syncSerial   int
	syncTimer    *time.Timer
	pgpDefaults  string
	keys         Keys
	editor       string
	split        string
	pageSize     int
	pgpIdentity  string
	openedDirs   []string
	running      bool
}

type event struct {
	status           string
	error            bool
	reload           bool
	accountsChanged  bool
	clearSelectionAt int
	autoSync         int
	syncDone         bool
}

func DefaultTheme() Theme {
	return Theme{
		Header:   ansiCodes("1", rgbFG("05070a"), rgbBG("60a5fa")),
		Label:    ansiCodes("2", rgbFG("8b949e")),
		Selected: ansiCodes(rgbFG("ffffff"), rgbBG("1f6feb")),
		Unread:   ansiCodes("1", rgbFG("f0f3f6")),
		Status:   ansiCodes(rgbFG("4ade80")),
		Error:    ansiCodes("1", rgbFG("ff6b7a")),
		Divider:  ansiCodes("2", rgbFG("8b949e")),
		Tag:      ansiCodes(rgbFG("c084fc")),
		Spam:     ansiCodes("1", rgbFG("facc15")),
		Dim:      ansiCodes("2", rgbFG("8b949e")),
		Link:     ansiCodes("4", rgbFG("60a5fa")),
	}
}

func ThemeFromConfig(cfg config.ThemeConfig) Theme {
	theme := DefaultTheme()
	if cfg.Header != "" {
		theme.Header = ansiStyle(cfg.Header, theme.Header)
	}
	if cfg.Label != "" {
		theme.Label = ansiStyle(cfg.Label, theme.Label)
	}
	if cfg.Selected != "" {
		theme.Selected = ansiStyle(cfg.Selected, theme.Selected)
	}
	if cfg.Unread != "" {
		theme.Unread = ansiStyle(cfg.Unread, theme.Unread)
	}
	if cfg.Status != "" {
		theme.Status = ansiStyle(cfg.Status, theme.Status)
	}
	if cfg.Error != "" {
		theme.Error = ansiStyle(cfg.Error, theme.Error)
	}
	if cfg.Divider != "" {
		theme.Divider = ansiStyle(cfg.Divider, theme.Divider)
	}
	if cfg.Tag != "" {
		theme.Tag = ansiStyle(cfg.Tag, theme.Tag)
	}
	if cfg.Spam != "" {
		theme.Spam = ansiStyle(cfg.Spam, theme.Spam)
	}
	if cfg.Dim != "" {
		theme.Dim = ansiStyle(cfg.Dim, theme.Dim)
	}
	if cfg.Link != "" {
		theme.Link = ansiStyle(cfg.Link, theme.Link)
	}
	return theme
}

func DefaultKeys() Keys {
	return Keys{
		Quit:         "q",
		ForceQuit:    "Q",
		Sync:         "s",
		Compose:      "c",
		Filter:       "f",
		Actions:      "space",
		Open:         "enter",
		Next:         "j",
		Prev:         "k",
		Down:         "down",
		Up:           "up",
		PageDown:     "pagedown",
		PageUp:       "pageup",
		Top:          "g",
		Bottom:       "G",
		CycleAccount: "a",
	}
}

func KeysFromConfig(cfg config.KeysConfig) Keys {
	keys := DefaultKeys()
	if cfg.Quit != "" {
		keys.Quit = normalizeKeyName(cfg.Quit)
	}
	if cfg.ForceQuit != "" {
		keys.ForceQuit = normalizeKeyName(cfg.ForceQuit)
	}
	if cfg.Sync != "" {
		keys.Sync = normalizeKeyName(cfg.Sync)
	}
	if cfg.Compose != "" {
		keys.Compose = normalizeKeyName(cfg.Compose)
	}
	if cfg.Filter != "" {
		keys.Filter = normalizeKeyName(cfg.Filter)
	}
	if cfg.Actions != "" {
		keys.Actions = normalizeKeyName(cfg.Actions)
	}
	if cfg.Open != "" {
		keys.Open = normalizeKeyName(cfg.Open)
	}
	if cfg.Next != "" {
		keys.Next = normalizeKeyName(cfg.Next)
	}
	if cfg.Prev != "" {
		keys.Prev = normalizeKeyName(cfg.Prev)
	}
	if cfg.Down != "" {
		keys.Down = normalizeKeyName(cfg.Down)
	}
	if cfg.Up != "" {
		keys.Up = normalizeKeyName(cfg.Up)
	}
	if cfg.PageDown != "" {
		keys.PageDown = normalizeKeyName(cfg.PageDown)
	}
	if cfg.PageUp != "" {
		keys.PageUp = normalizeKeyName(cfg.PageUp)
	}
	if cfg.Top != "" {
		keys.Top = normalizeKeyName(cfg.Top)
	}
	if cfg.Bottom != "" {
		keys.Bottom = normalizeKeyName(cfg.Bottom)
	}
	if cfg.CycleAccount != "" {
		keys.CycleAccount = normalizeKeyName(cfg.CycleAccount)
	}
	return keys
}

func applyTheme(theme Theme) {
	defaults := DefaultTheme()
	if theme.Header == "" {
		theme.Header = defaults.Header
	}
	if theme.Label == "" {
		theme.Label = defaults.Label
	}
	if theme.Selected == "" {
		theme.Selected = defaults.Selected
	}
	if theme.Unread == "" {
		theme.Unread = defaults.Unread
	}
	if theme.Status == "" {
		theme.Status = defaults.Status
	}
	if theme.Error == "" {
		theme.Error = defaults.Error
	}
	if theme.Divider == "" {
		theme.Divider = defaults.Divider
	}
	if theme.Tag == "" {
		theme.Tag = defaults.Tag
	}
	if theme.Spam == "" {
		theme.Spam = defaults.Spam
	}
	if theme.Dim == "" {
		theme.Dim = defaults.Dim
	}
	if theme.Link == "" {
		theme.Link = defaults.Link
	}
	styleHeader = theme.Header
	styleLabel = theme.Label
	styleSelected = theme.Selected
	styleUnread = theme.Unread
	styleStatus = theme.Status
	styleError = theme.Error
	styleDivider = theme.Divider
	styleTag = theme.Tag
	styleSpam = theme.Spam
	styleDim = theme.Dim
	styleLink = theme.Link
}

func ansiStyle(spec, fallback string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return fallback
	}
	if strings.Contains(spec, "\x1b[") {
		return spec
	}
	codes := []string{}
	for _, token := range strings.FieldsFunc(strings.ToLower(spec), func(r rune) bool {
		return r == ' ' || r == ',' || r == '+' || r == '|'
	}) {
		if code := colorCode(token); code != "" {
			codes = append(codes, code)
			continue
		}
		switch token {
		case "none", "normal", "reset", "default":
			return ""
		case "bold":
			codes = append(codes, "1")
		case "dim":
			codes = append(codes, "2")
		case "italic":
			codes = append(codes, "3")
		case "underline", "under":
			codes = append(codes, "4")
		case "reverse", "inverse":
			codes = append(codes, "7")
		case "black":
			codes = append(codes, "30")
		case "red":
			codes = append(codes, "31")
		case "green":
			codes = append(codes, "32")
		case "yellow":
			codes = append(codes, "33")
		case "blue":
			codes = append(codes, "34")
		case "magenta":
			codes = append(codes, "35")
		case "cyan":
			codes = append(codes, "36")
		case "white":
			codes = append(codes, "37")
		case "bright-black", "gray", "grey":
			codes = append(codes, "90")
		case "bright-red":
			codes = append(codes, "91")
		case "bright-green":
			codes = append(codes, "92")
		case "bright-yellow":
			codes = append(codes, "93")
		case "bright-blue":
			codes = append(codes, "94")
		case "bright-magenta":
			codes = append(codes, "95")
		case "bright-cyan":
			codes = append(codes, "96")
		case "bright-white":
			codes = append(codes, "97")
		}
	}
	if len(codes) == 0 {
		return fallback
	}
	return ansiCodes(codes...)
}

func ansiCodes(codes ...string) string {
	out := []string{}
	for _, code := range codes {
		if code != "" {
			out = append(out, code)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(out, ";") + "m"
}

func colorCode(token string) string {
	key, value, ok := strings.Cut(token, "=")
	if !ok {
		key, value, ok = strings.Cut(token, ":")
	}
	if !ok {
		value = token
		key = "fg"
	}
	value = strings.TrimPrefix(value, "#")
	if len(value) != 6 || strings.Trim(value, "0123456789abcdef") != "" {
		return ""
	}
	switch key {
	case "fg", "foreground":
		return rgbFG(value)
	case "bg", "background":
		return rgbBG(value)
	default:
		return ""
	}
}

func rgbFG(hex string) string { return rgbCode("38", hex) }

func rgbBG(hex string) string { return rgbCode("48", hex) }

func rgbCode(prefix, hex string) string {
	if len(hex) != 6 {
		return ""
	}
	r, errR := strconv.ParseUint(hex[0:2], 16, 8)
	g, errG := strconv.ParseUint(hex[2:4], 16, 8)
	b, errB := strconv.ParseUint(hex[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return ""
	}
	return fmt.Sprintf("%s;2;%d;%d;%d", prefix, r, g, b)
}

func normalizeKeyName(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) == 1 {
		return value
	}
	return strings.ToLower(value)
}

func Run(s *store.Store, accounts *store.AccountStore, options Options) error {
	applyTheme(options.Theme)
	keys := options.Keys
	if keys.Quit == "" {
		keys = DefaultKeys()
	}
	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	app := &App{
		store:       s,
		accounts:    accounts,
		events:      make(chan event, 16),
		dirty:       true,
		running:     true,
		pgpDefaults: options.PGPDefaults,
		pgpIdentity: strings.TrimSpace(options.PGPIdentity),
		keys:        keys,
		editor:      options.Editor,
		split:       strings.ToLower(strings.TrimSpace(options.Split)),
		pageSize:    pageSize,
	}
	app.reloadAccounts()
	app.reload()
	app.sync()
	return app.run()
}

func (a *App) run() error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("tui needs interactive terminal: %w", err)
	}
	defer tty.Close()
	oldStdin, oldStdout, oldStderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = tty, tty, tty
	defer func() {
		os.Stdin, os.Stdout, os.Stderr = oldStdin, oldStdout, oldStderr
	}()
	if err := rawMode(); err != nil {
		return err
	}
	defer func() {
		_ = cookedMode()
		disableMouse()
		a.stopSyncTimer()
		a.cleanupOpenedAttachments()
		fmt.Print("\x1b[?25h\x1b[?1049l\x1b[2J\x1b[H")
		_ = a.store.Flush()
	}()
	fmt.Print("\x1b[?1049h\x1b[?25l")
	enableMouse()
	for a.running {
		if a.drainEvents() {
			a.dirty = true
		}
		if a.dirty {
			a.draw()
			a.dirty = false
		}
		key, err := readKey()
		if err != nil {
			return err
		}
		if key == "" {
			continue
		}
		if a.handle(key) {
			a.dirty = true
		}
	}
	return nil
}

func (a *App) drainEvents() bool {
	dirty := false
	for {
		select {
		case event := <-a.events:
			dirty = true
			if event.autoSync != 0 {
				if event.autoSync == a.syncSerial && !a.syncRunning {
					a.sync()
				}
				continue
			}
			if event.syncDone {
				a.syncRunning = false
				a.reloadAccounts()
				a.scheduleAutoSync()
			}
			if event.reload {
				a.reload()
			}
			if event.accountsChanged {
				a.reloadAccounts()
			}
			if event.clearSelectionAt != 0 && event.clearSelectionAt == a.selectToken && !a.selectActive {
				a.clearSelection()
			}
			if event.status != "" {
				a.status = event.status
				a.error = event.error
			}
			if event.status == "" && event.error {
				a.error = true
			}
		default:
			return dirty
		}
	}
}

func (a *App) reloadAccounts() {
	if a.accounts == nil {
		return
	}
	items, err := a.accounts.All()
	if err == nil {
		a.accountsList = items
	}
}

func (a *App) reload() {
	messages := a.filteredSourceMessages()
	filtered := messages[:0]
	for _, msg := range messages {
		if a.account > 0 && a.account-1 < len(a.accountsList) && msg.AccountID != a.accountsList[a.account-1].ID {
			continue
		}
		switch a.filter {
		case "read":
			if !msg.Read {
				continue
			}
		case "unread":
			if msg.Read {
				continue
			}
		case "spam":
			if !msg.Spam {
				continue
			}
		case "sent":
			if !msg.IsSent() {
				continue
			}
		case "drafts":
			if !msg.IsDraft() {
				continue
			}
		case "today":
			if !strings.HasPrefix(msg.ReceivedAt, time.Now().Format("2006-01-02")) {
				continue
			}
		}
		filtered = append(filtered, msg)
	}
	a.messages = filtered
	if a.selected >= len(a.messages) {
		a.selected = len(a.messages) - 1
	}
	if a.selected < 0 {
		a.selected = 0
	}
}

func (a *App) filteredSourceMessages() []*store.Message {
	switch a.filter {
	case "spam":
		return a.store.MessagesAll(true, false)
	case "sent":
		return a.store.MessagesAll(false, true)
	case "drafts":
		return a.store.Drafts()
	case "search":
		return a.store.Search(a.filterQuery, false, false, true)
	default:
		return a.store.Messages(false)
	}
}

func (a *App) handle(key string) bool {
	if strings.HasPrefix(key, "mouse:") {
		return a.handleMouse(key)
	}
	if a.actionScope == "mail" {
		a.handleMailAction(key)
		return true
	}
	if a.actionScope == "filter" {
		a.handleFilterAction(key)
		return true
	}
	if a.actionScope == "link" {
		a.handleLinkAction(key)
		return true
	}
	switch {
	case a.keyIs(key, a.keys.Quit):
		if a.preview != nil || a.filter != "" {
			a.preview = nil
			a.filter = ""
			a.filterQuery = ""
			a.status = ""
			a.reload()
			return true
		}
		a.running = false
	case a.keyIs(key, a.keys.ForceQuit):
		a.running = false
	case a.keyIs(key, a.keys.Next, a.keys.Down):
		a.move(1)
	case a.keyIs(key, a.keys.Prev, a.keys.Up):
		a.move(-1)
	case a.keyIs(key, a.keys.PageDown):
		a.page(1)
	case a.keyIs(key, a.keys.PageUp):
		a.page(-1)
	case a.keyIs(key, a.keys.Top):
		a.selected = 0
	case a.keyIs(key, a.keys.Bottom):
		if len(a.messages) > 0 {
			a.selected = len(a.messages) - 1
		}
	case a.keyIs(key, a.keys.Open):
		a.openSelected()
	case a.keyIs(key, a.keys.Actions):
		if a.current() != nil {
			a.actionScope = "mail"
			a.status = ""
		}
	case a.keyIs(key, a.keys.Filter):
		a.actionScope = "filter"
		a.status = ""
	case key == "/":
		a.searchPrompt()
	case a.keyIs(key, a.keys.Sync):
		a.sync()
	case a.keyIs(key, a.keys.Compose):
		a.compose(nil, false, false)
	case a.keyIs(key, a.keys.CycleAccount):
		if len(a.accountsList) > 0 {
			a.account = (a.account + 1) % (len(a.accountsList) + 1)
			a.selected = 0
			a.reload()
		}
	default:
		return false
	}
	return true
}

func (a *App) keyIs(key string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && key == candidate {
			return true
		}
	}
	return false
}

func (a *App) handleMailAction(key string) {
	msg := a.currentAction()
	if key == "esc" || key == "q" || key == "space" {
		a.actionScope = ""
		a.status = ""
		return
	}
	if msg == nil {
		a.statusError("no selected mail")
		return
	}
	closeScope := true
	defer func() {
		if closeScope {
			a.actionScope = ""
		}
	}()
	switch key {
	case "r":
		a.compose(msg, false, false)
	case "R":
		a.compose(msg, true, false)
	case "f":
		a.compose(msg, false, true)
	case "h":
		a.headerMode = !a.headerMode
		a.status = ""
	case "a":
		a.attachmentMenu(msg)
	case "u":
		msg.SetRead(false)
		a.reload()
		a.status = "marked unread"
	case "t":
		msg.MarkTrashed()
		a.preview = nil
		a.reload()
		a.status = "moved to trash"
	case "s":
		msg.SetSpam(!msg.Spam)
		a.reload()
		if msg.Spam {
			a.status = "marked spam"
		} else {
			a.status = "marked not spam"
		}
	default:
		closeScope = false
		a.statusError("unknown mail action: " + key)
	}
}

func (a *App) handleFilterAction(key string) {
	switch key {
	case "esc", "q", "f":
		a.actionScope = ""
		a.status = ""
	case "c":
		a.filter = ""
		a.filterQuery = ""
		a.actionScope = ""
		a.reload()
		a.status = ""
	case "s":
		a.setFilter("spam")
	case "e":
		a.setFilter("sent")
	case "D":
		a.setFilter("drafts")
	case "r":
		a.setFilter("read")
	case "u":
		a.setFilter("unread")
	case "d":
		a.setFilter("today")
	default:
		a.statusError("unknown filter: " + key)
	}
}

func (a *App) handleLinkAction(key string) {
	url := a.pendingURL
	switch key {
	case "enter":
		if url != "" {
			if err := openExternal(url); err != nil {
				a.statusError(err.Error())
			} else {
				a.status = "opened " + url
			}
		}
		a.pendingURL = ""
		a.actionScope = ""
	case "c":
		if url != "" {
			a.status = copyToClipboard(url)
		}
		a.pendingURL = ""
		a.actionScope = ""
	case "esc", "q", "x":
		a.pendingURL = ""
		a.actionScope = ""
		a.status = ""
	default:
		a.status = "link: " + url + "  enter open  c copy  esc cancel"
	}
}

func (a *App) searchPrompt() {
	query, err := a.promptLine("search")
	if err != nil {
		a.statusError(err.Error())
		return
	}
	query = strings.TrimSpace(query)
	if query == "" {
		a.status = ""
		return
	}
	a.filter = "search"
	a.filterQuery = query
	a.actionScope = ""
	a.selected = 0
	a.scroll = 0
	a.reload()
	a.status = fmt.Sprintf("filter: search %s (%d matches)", query, len(a.messages))
}

func (a *App) handleMouse(key string) bool {
	var button, x, y int
	var release string
	if _, err := fmt.Sscanf(key, "mouse:%d:%d:%d:%s", &button, &x, &y, &release); err != nil {
		return false
	}
	if button&64 != 0 && button&1 == 0 {
		if a.preview != nil && !pointInArea(x, y, a.listArea) {
			a.scrollBody(-3)
		} else {
			a.scrollList(-3)
		}
		return true
	}
	if button&64 != 0 && button&1 == 1 {
		if a.preview != nil && !pointInArea(x, y, a.listArea) {
			a.scrollBody(3)
		} else {
			a.scrollList(3)
		}
		return true
	}
	if a.preview != nil && pointInArea(x, y, a.bodyArea) {
		if release == "release" {
			if a.selectActive {
				a.updateSelection(x, y)
				if samePoint(a.selectStart, a.selectEnd) {
					if url := a.linkAtBodyPoint(x, y); url != "" {
						a.confirmLink(url)
						return true
					}
				}
				a.finishSelection()
				return true
			}
			return false
		}
		if button == 0 {
			a.startSelection(x, y)
			return true
		}
		if button&32 != 0 && a.selectActive {
			a.updateSelection(x, y)
			return true
		}
	}
	if button&3 != 0 || release == "release" {
		return false
	}
	if pointInArea(x, y, a.listArea) {
		index := a.scroll + (y - a.listArea.y)
		if index >= 0 && index < len(a.messages) {
			a.selected = index
			a.actionScope = ""
			a.openSelected()
			return true
		}
	}
	return false
}

func (a *App) startSelection(x, y int) {
	p := a.bodyPoint(x, y)
	a.selectStart = &p
	a.selectEnd = &p
	a.selectActive = true
	a.selectToken++
}

func (a *App) updateSelection(x, y int) {
	p := a.bodyPoint(x, y)
	a.selectEnd = &p
}

func (a *App) finishSelection() {
	a.selectActive = false
	text := a.selectedText()
	if strings.TrimSpace(text) == "" {
		a.clearSelection()
		return
	}
	a.status = copyToClipboard(text)
	token := a.selectToken
	go func() {
		time.Sleep(500 * time.Millisecond)
		a.events <- event{clearSelectionAt: token}
	}()
}

func (a *App) clearSelection() {
	a.selectStart = nil
	a.selectEnd = nil
	a.selectActive = false
	a.selectToken++
}

func samePoint(a, b *point) bool {
	return a != nil && b != nil && a.line == b.line && a.col == b.col
}

func (a *App) linkAtBodyPoint(x, y int) string {
	if a.preview == nil || !pointInArea(x, y, a.bodyArea) {
		return ""
	}
	content := a.previewBody
	if a.headerMode {
		content = a.previewHead
	}
	lines := previewLines(a.preview, content, a.bodyArea.w)
	lineIndex := a.bodyScroll + max(0, min(max(0, a.bodyArea.h-1), y-a.bodyArea.y))
	if lineIndex < 0 || lineIndex >= len(lines) {
		return ""
	}
	col := max(0, min(max(0, a.bodyArea.w-1), x-a.bodyArea.x))
	for _, link := range previewLinks(lines[lineIndex]) {
		if col >= link.start && col <= link.end {
			return link.url
		}
	}
	return ""
}

func (a *App) confirmLink(url string) {
	a.clearSelection()
	a.pendingURL = url
	a.actionScope = "link"
	a.status = "link: " + url + "  enter open  c copy  esc cancel"
}

func (a *App) bodyPoint(x, y int) point {
	line := a.bodyScroll + max(0, min(max(0, a.bodyArea.h-1), y-a.bodyArea.y))
	col := max(0, min(max(0, a.bodyArea.w-1), x-a.bodyArea.x))
	return point{line: line, col: col}
}

func (a *App) selectedText() string {
	if a.selectStart == nil || a.selectEnd == nil || a.preview == nil {
		return ""
	}
	start, end := *a.selectStart, *a.selectEnd
	if pointLess(end, start) {
		start, end = end, start
	}
	content := a.previewBody
	if a.headerMode {
		content = a.previewHead
	}
	lines := previewLines(a.preview, content, a.bodyArea.w)
	chunks := []string{}
	for i := start.line; i <= end.line && i < len(lines); i++ {
		text := previewPlainText(lines[i])
		left := 0
		right := displayLen(text)
		if i == start.line {
			left = min(start.col, right)
		}
		if i == end.line {
			right = min(end.col, right)
		}
		if right >= left {
			chunks = append(chunks, sliceRunes(text, left, right))
		}
	}
	return strings.TrimRight(strings.Join(chunks, "\n"), "\n")
}

func pointLess(a, b point) bool {
	return a.line < b.line || (a.line == b.line && a.col < b.col)
}

func (a *App) scrollList(delta int) {
	if len(a.messages) == 0 || a.listArea.h <= 0 {
		return
	}
	maxScroll := max(0, len(a.messages)-a.listArea.h)
	a.scroll += delta
	if a.scroll < 0 {
		a.scroll = 0
	}
	if a.scroll > maxScroll {
		a.scroll = maxScroll
	}
	if a.selected < a.scroll {
		a.selected = a.scroll
	}
	bottom := min(len(a.messages)-1, a.scroll+a.listArea.h-1)
	if a.selected > bottom {
		a.selected = bottom
	}
}

func (a *App) scrollBody(delta int) {
	if a.preview == nil || a.bodyArea.h <= 0 {
		return
	}
	content := a.previewBody
	if a.headerMode {
		content = a.previewHead
	}
	maxScroll := max(0, len(previewLines(a.preview, content, a.bodyArea.w))-a.bodyArea.h)
	a.bodyScroll += delta
	if a.bodyScroll < 0 {
		a.bodyScroll = 0
	}
	if a.bodyScroll > maxScroll {
		a.bodyScroll = maxScroll
	}
}

func pointInArea(x, y int, value area) bool {
	return value.h > 0 && value.w > 0 && x >= value.x && x < value.x+value.w && y >= value.y && y < value.y+value.h
}

func (a *App) setFilter(filter string) {
	a.filter = filter
	a.filterQuery = ""
	a.actionScope = ""
	a.selected = 0
	a.scroll = 0
	a.reload()
	a.status = fmt.Sprintf("filter: %s (%d matches)", filter, len(a.messages))
}

func (a *App) move(delta int) {
	if len(a.messages) == 0 {
		return
	}
	a.selected += delta
	if a.selected < 0 {
		a.selected = 0
	}
	if a.selected >= len(a.messages) {
		a.selected = len(a.messages) - 1
	}
}

func (a *App) page(delta int) {
	if a.preview != nil {
		a.bodyScroll += delta * 10
		if a.bodyScroll < 0 {
			a.bodyScroll = 0
		}
		return
	}
	a.move(delta * 10)
}

func (a *App) current() *store.Message {
	if len(a.messages) == 0 || a.selected < 0 || a.selected >= len(a.messages) {
		return nil
	}
	return a.messages[a.selected]
}

func (a *App) currentAction() *store.Message {
	if a.preview != nil {
		return a.preview
	}
	return a.current()
}

func (a *App) openSelected() {
	msg := a.current()
	if msg == nil {
		return
	}
	a.status = ""
	a.error = false
	if !msg.IsDraft() {
		msg.SetRead(true)
	}
	body, err := a.store.OpenBody(msg)
	if err != nil {
		a.statusError(err.Error())
		return
	}
	a.preview = msg
	a.previewHead = body.Headers
	text, status, _ := pgp.ProcessTextWithKeys(body.Text, a.publicKeyAttachments(msg))
	a.previewBody = text
	a.headerMode = false
	a.bodyScroll = 0
	a.clearSelection()
	a.reload()
	if status != "" {
		a.status = status
	}
}

func (a *App) sync() {
	if len(a.accountsList) == 0 {
		a.statusError("no accounts")
		return
	}
	if a.syncRunning {
		a.status = "sync already running"
		return
	}
	a.stopSyncTimer()
	a.syncRunning = true
	accounts := append([]store.Account(nil), a.accountsList...)
	a.status = fmt.Sprintf("syncing 0/%d accounts...", len(accounts))
	go func() {
		defer a.reportPanic()
		defer func() { a.events <- event{syncDone: true} }()
		total := 0
		post := func(status string) {
			a.events <- event{status: status}
		}
		for i, account := range accounts {
			prefix := fmt.Sprintf("sync %d/%d %s", i+1, len(accounts), accountLabel(account))
			post(prefix + ": starting")
			progress := func(message string) {
				post(prefix + ": " + message)
			}
			var count int
			var err error
			switch account.Protocol {
			case "imap", "imaps", "exchange-online":
				count, err = protocol.SyncIMAPSWithUpdater(account, a.store, a.pageSize, progress, a.accounts.Upsert)
			case "jmap":
				count, err = protocol.SyncJMAP(account, a.store, a.pageSize, progress)
			}
			if err != nil {
				a.events <- event{status: err.Error(), error: true}
				return
			}
			total += count
			post(fmt.Sprintf("%s: synced %d new messages", prefix, count))
		}
		a.events <- event{status: fmt.Sprintf("synced %d messages", total), reload: true}
	}()
}

func (a *App) scheduleAutoSync() {
	a.stopSyncTimer()
	a.syncSerial++
	serial := a.syncSerial
	a.syncTimer = time.AfterFunc(5*time.Minute, func() {
		a.events <- event{autoSync: serial}
	})
}

func (a *App) stopSyncTimer() {
	if a.syncTimer != nil {
		a.syncTimer.Stop()
		a.syncTimer = nil
	}
}

func accountLabel(account store.Account) string {
	if account.Email != "" {
		return account.Email
	}
	return account.ID
}

func (a *App) compose(source *store.Message, replyAll bool, forward bool) {
	account, ok := a.bestComposeAccount(source)
	if !ok {
		a.statusError("no account")
		return
	}
	draft := protocol.Draft{From: account.Email, PGP: a.pgpDefaults}
	if source != nil {
		sourceText := a.previewBody
		if a.preview == nil || a.preview.Key != source.Key || sourceText == "" {
			body, err := a.store.OpenBody(source)
			if err != nil {
				a.statusError(err.Error())
				return
			}
			sourceText, _, _ = pgp.ProcessTextWithKeys(body.Text, a.publicKeyAttachments(source))
		}
		if forward {
			draft.Subject = prefixedSubject("Fwd:", source.Subject)
			draft.Body = "\n\n---------- Forwarded message ---------\nFrom: " + source.From + "\nDate: " + firstNonEmpty(source.ReceivedAt, source.SentAt) + "\nSubject: " + source.Subject + "\nTo: " + strings.Join(source.To, ", ") + "\n\n" + sourceText + "\n"
		} else {
			draft.To, draft.Cc = replyAddresses(source, account.Email, replyAll)
			draft.Subject = prefixedSubject("Re:", source.Subject)
			draft.Body = "\n\n" + replyAttribution(source) + "\n" + quote(sourceText) + "\n"
		}
	}
	for {
		path, err := compose.WriteDraftFile(draft)
		if err != nil {
			a.statusError(err.Error())
			return
		}
		if err := a.runEditor(path); err != nil {
			_ = os.Remove(path)
			a.statusError(err.Error())
			return
		}
		parsed, err := compose.ReadDraftFile(path)
		_ = os.Remove(path)
		if err != nil {
			a.statusError(err.Error())
			return
		}
		if strings.TrimSpace(parsed.From) == "" {
			parsed.From = draft.From
		}
		parsed.Attachments = draft.Attachments
		if strings.TrimSpace(parsed.PGP) == "" {
			parsed.PGP = draft.PGP
		}
		sendAccount, err := a.sendAccountForDraft(account, &parsed)
		if err != nil {
			a.statusError(err.Error())
			return
		}
		choice := a.confirmCompose(sendAccount, &parsed)
		switch choice {
		case "edit":
			draft = parsed
			account = sendAccount
			continue
		case "draft":
			if err := a.store.SaveDraft(sendAccount.ID, parsed.From, parsed.To, parsed.Cc, parsed.Bcc, parsed.Subject, parsed.Body); err != nil {
				a.statusError(err.Error())
			} else {
				a.reload()
				a.status = "saved local encrypted draft"
			}
			return
		case "send":
			if compose.EmptyRecipient(parsed) {
				a.status = "compose cancelled: no recipient"
				return
			}
			a.sendDraft(sendAccount, parsed)
			return
		default:
			a.status = "compose cancelled"
			return
		}
	}
}

func (a *App) sendDraft(account store.Account, parsed protocol.Draft) {
	a.status = "sending..."
	go func(account store.Account, parsed protocol.Draft) {
		defer a.reportPanic()
		pgpStatus := ""
		parsed, pgpStatus, err := pgp.ApplyDraft(a.draftPGPIdentity(parsed), parsed)
		if err != nil {
			a.events <- event{status: err.Error(), error: true}
			return
		}
		accountsChanged := false
		if account.Protocol == "jmap" {
			err = protocol.SendJMAP(account, parsed)
		} else {
			err = protocol.SendSMTPSWithUpdater(account, parsed, func(updated store.Account) error {
				if err := a.accounts.Upsert(updated); err != nil {
					return err
				}
				accountsChanged = true
				return nil
			})
		}
		if err != nil {
			a.events <- event{status: err.Error(), error: true}
			return
		}
		_, _ = a.store.ImportSent(account.ID, []byte(protocol.Message(account, parsed)))
		a.store.RememberAddressStrings(parsed.From, parsed.To, parsed.Cc, parsed.Bcc)
		_ = a.store.Flush()
		if pgpStatus != "" {
			a.events <- event{status: "sent; " + pgpStatus, accountsChanged: accountsChanged}
		} else {
			a.events <- event{status: "sent", accountsChanged: accountsChanged}
		}
	}(account, parsed)
}

func (a *App) attachmentMenu(msg *store.Message) {
	attachments, err := a.store.Attachments(msg)
	if err != nil {
		a.statusError(err.Error())
		return
	}
	if len(attachments) == 0 {
		a.statusError("no attachments")
		return
	}
	selected := 0
	for {
		w, h := terminalSize()
		fmt.Print("\x1b[2J")
		printStyledLine(0, 0, w, " murat | attachments ", styleHeader)
		printStyledLine(h-1, 0, w, "enter open  v view text  i import pubkey  s save  q back", styleHeader)
		limit := max(0, h-2)
		for row := 0; row < limit && row < len(attachments); row++ {
			att := attachments[row]
			line := fmt.Sprintf("%s  %s %dB", att.Filename, att.ContentType, att.Size)
			style := ""
			if row == selected {
				style = styleSelected
			}
			printStyledLine(row+1, 0, w, line, style)
		}
		key, err := readKeyBlocking()
		if err != nil {
			a.statusError(err.Error())
			return
		}
		switch key {
		case "q", "esc":
			a.dirty = true
			return
		case "j", "down":
			selected = min(len(attachments)-1, selected+1)
		case "k", "up":
			selected = max(0, selected-1)
		case "enter", "o":
			a.openAttachment(attachments[selected])
			a.dirty = true
			return
		case "v":
			a.viewAttachment(msg, attachments[selected])
			a.dirty = true
			return
		case "i":
			a.importAttachmentKey(attachments[selected])
			a.dirty = true
			return
		case "s":
			a.saveAttachment(attachments[selected])
			a.dirty = true
			return
		}
	}
}

func (a *App) viewAttachment(msg *store.Message, att store.Attachment) {
	if !isTextAttachment(att) {
		a.statusError("not a text attachment")
		return
	}
	a.preview = msg
	a.previewBody = decodeBytesForUI(att.Data, att.ContentType)
	a.previewHead = ""
	a.headerMode = false
	a.bodyScroll = 0
	a.clearSelection()
	a.status = "viewing attachment: " + att.Filename
}

func (a *App) openAttachment(att store.Attachment) {
	dir, err := a.openAttachmentDir()
	if err != nil {
		a.statusError(err.Error())
		return
	}
	path := filepath.Join(dir, safeName(att.Filename))
	if err := os.WriteFile(path, att.Data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		a.statusError(err.Error())
		return
	}
	if err := openExternal(path); err != nil {
		_ = os.RemoveAll(dir)
		a.statusError(err.Error())
		return
	}
	a.openedDirs = append(a.openedDirs, dir)
	a.status = "opened " + filepath.Base(path)
}

func (a *App) openAttachmentDir() (string, error) {
	parent := filepath.Join(userdirs.Cache(), "murat", "attachments")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "open-*")
}

func (a *App) cleanupOpenedAttachments() {
	for _, dir := range a.openedDirs {
		_ = os.RemoveAll(dir)
	}
	a.openedDirs = nil
}

func openExternal(target string) error {
	cmdName := "xdg-open"
	if _, err := exec.LookPath(cmdName); err != nil {
		cmdName = "open"
	}
	return exec.Command(cmdName, target).Start()
}

func (a *App) importAttachmentKey(att store.Attachment) {
	cmd := exec.Command("gpg", "--batch", "--yes", "--import")
	cmd.Stdin = bytes.NewReader(att.Data)
	if err := cmd.Run(); err != nil {
		a.statusError("gpg import failed: " + err.Error())
		return
	}
	a.status = "imported public key"
}

func (a *App) publicKeyAttachments(msg *store.Message) [][]byte {
	if msg == nil || !msg.HasAttachment {
		return nil
	}
	attachments, err := a.store.Attachments(msg)
	if err != nil {
		return nil
	}
	return publicKeyAttachmentData(attachments)
}

func publicKeyAttachmentData(attachments []store.Attachment) [][]byte {
	keys := [][]byte{}
	for _, attachment := range attachments {
		if pgp.IsPublicKeyAttachment(attachment.Filename, attachment.ContentType, attachment.Data) {
			keys = append(keys, attachment.Data)
		}
	}
	return keys
}

func (a *App) saveAttachment(att store.Attachment) {
	defaultPath := filepath.Join(userdirs.Downloads(), safeName(att.Filename))
	target, cancelled, err := a.promptPath("save as", defaultPath)
	if err != nil {
		a.statusError(err.Error())
		return
	}
	if cancelled {
		a.status = ""
		return
	}
	target = strings.TrimSpace(target)
	usingDefault := false
	if target == "" {
		target = defaultPath
		usingDefault = true
	} else {
		target = userdirs.Expand(target)
	}
	if usingDefault {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			a.statusError(err.Error())
			return
		}
	}
	if stat, err := os.Stat(target); err == nil && stat.IsDir() {
		target = filepath.Join(target, safeName(att.Filename))
	}
	if err := os.WriteFile(target, att.Data, 0o600); err != nil {
		a.statusError(err.Error())
		return
	}
	a.status = "saved " + target
}

func (a *App) reportPanic() {
	if value := recover(); value != nil {
		a.events <- event{status: fmt.Sprintf("panic: %v", value), error: true}
	}
}

func (a *App) confirmCompose(account store.Account, draft *protocol.Draft) string {
	scroll := 0
	pgpMenu := false
	notice := ""
	availability := pgp.CheckAvailability(a.draftPGPIdentity(*draft), *draft)
	restrictPGP(draft, availability)
	for {
		w, h := terminalSize()
		fmt.Print("\x1b[2J")
		printStyledLine(0, 0, w, " murat | compose preview ", styleHeader)
		statusLine := composePGPLine(*draft, availability, pgpMenu)
		statusStyle := styleStatus
		if notice != "" && !pgpMenu {
			statusLine = notice
			if strings.HasPrefix(notice, "ERROR:") {
				statusStyle = styleError
			}
		}
		printStyledLine(h-2, 0, w, statusLine, statusStyle)
		printStyledLine(h-1, 0, w, composeShortcuts(*draft, availability, pgpMenu), styleHeader)
		lines := strings.Split(formatDraftPreview(*draft), "\n")
		bodyHeight := max(0, h-3)
		if scroll > max(0, len(lines)-bodyHeight) {
			scroll = max(0, len(lines)-bodyHeight)
		}
		for row := 0; row < bodyHeight && scroll+row < len(lines); row++ {
			printLine(row+1, 0, w, lines[scroll+row])
		}
		key, err := readKeyBlocking()
		if err != nil {
			a.statusError(err.Error())
			return "cancel"
		}
		if pgpMenu {
			if handleComposePGPKey(key, draft, availability) {
				pgpMenu = false
			}
			continue
		}
		switch key {
		case "s", "enter":
			if len(draft.Attachments) > 0 && pgpBodyProtectionEnabled(draft.PGP) {
				notice = "ERROR: PGP encrypt/sign with file attachments not supported"
				continue
			}
			a.dirty = true
			return "send"
		case "d":
			if len(draft.Attachments) > 0 {
				notice = "ERROR: attached files cannot be saved in local drafts"
				continue
			}
			a.dirty = true
			return "draft"
		case "e":
			a.dirty = true
			return "edit"
		case "a":
			notice = a.attachDraftFile(draft)
			scroll = min(scroll, max(0, len(strings.Split(formatDraftPreview(*draft), "\n"))-bodyHeight))
		case "A":
			notice = detachLastDraftAttachment(draft)
			scroll = min(scroll, max(0, len(strings.Split(formatDraftPreview(*draft), "\n"))-bodyHeight))
		case "x", "q", "esc":
			a.dirty = true
			return "cancel"
		case "g":
			notice = ""
			if anyPGPAvailable(availability) {
				pgpMenu = true
			}
		case "j", "down":
			notice = ""
			scroll = min(max(0, len(lines)-bodyHeight), scroll+1)
		case "k", "up":
			notice = ""
			scroll = max(0, scroll-1)
		case "pagedown":
			notice = ""
			scroll = min(max(0, len(lines)-bodyHeight), scroll+bodyHeight)
		case "pageup":
			notice = ""
			scroll = max(0, scroll-bodyHeight)
		}
	}
}

func (a *App) draftPGPIdentity(draft protocol.Draft) string {
	from := draftSenderEmail(draft.From)
	if strings.TrimSpace(a.pgpIdentity) != "" && (from == "" || !pgp.HasSecretKey(from)) {
		return strings.TrimSpace(a.pgpIdentity)
	}
	return from
}

func (a *App) attachDraftFile(draft *protocol.Draft) string {
	path, cancelled, err := a.promptFile("attach file", userdirs.Downloads())
	if err != nil {
		return "ERROR: " + err.Error()
	}
	if cancelled || strings.TrimSpace(path) == "" {
		return ""
	}
	attachment, err := draftAttachmentFromPath(path)
	if err != nil {
		return "ERROR: " + err.Error()
	}
	draft.Attachments = append(draft.Attachments, attachment)
	return "attached " + attachment.Filename
}

func detachLastDraftAttachment(draft *protocol.Draft) string {
	if len(draft.Attachments) == 0 {
		return "no attachments"
	}
	last := draft.Attachments[len(draft.Attachments)-1]
	draft.Attachments = draft.Attachments[:len(draft.Attachments)-1]
	name := strings.TrimSpace(last.Filename)
	if name == "" {
		name = "attachment"
	}
	return "detached " + name
}

func draftAttachmentFromPath(path string) (protocol.Attachment, error) {
	path = userdirs.Expand(strings.TrimSpace(path))
	if path == "" {
		return protocol.Attachment{}, fmt.Errorf("attachment path required")
	}
	stat, err := os.Stat(path)
	if err != nil {
		return protocol.Attachment{}, err
	}
	if stat.IsDir() {
		return protocol.Attachment{}, fmt.Errorf("choose a file, not a directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return protocol.Attachment{}, err
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	return protocol.Attachment{Filename: filepath.Base(path), ContentType: contentType, Data: data}, nil
}

func (a *App) defaultSendAccount() (store.Account, bool) {
	if a.account > 0 && a.account-1 < len(a.accountsList) {
		return a.accountsList[a.account-1], true
	}
	if len(a.accountsList) == 0 {
		return store.Account{}, false
	}
	return a.accountsList[0], true
}

func (a *App) bestComposeAccount(source *store.Message) (store.Account, bool) {
	if len(a.accountsList) == 0 {
		return store.Account{}, false
	}
	if source != nil {
		for _, account := range a.accountsList {
			if source.AccountID != "" && source.AccountID == account.ID {
				return account, true
			}
		}
		for _, account := range a.accountsList {
			if messageAddressListContains(source.To, account.Email) || messageAddressListContains(source.Cc, account.Email) {
				return account, true
			}
		}
	}
	return a.defaultSendAccount()
}

func (a *App) sendAccountForDraft(fallback store.Account, draft *protocol.Draft) (store.Account, error) {
	if strings.TrimSpace(draft.From) == "" {
		draft.From = fallback.Email
		return fallback, nil
	}
	email := draftSenderEmail(draft.From)
	if email == "" {
		return store.Account{}, fmt.Errorf("from address required")
	}
	if strings.EqualFold(email, fallback.Email) {
		return fallback, nil
	}
	for _, account := range a.accountsList {
		if strings.EqualFold(email, account.Email) || strings.EqualFold(email, account.ID) {
			if !strings.Contains(email, "@") {
				draft.From = account.Email
			}
			return account, nil
		}
	}
	return store.Account{}, fmt.Errorf("account not found for from: %s", draft.From)
}

func draftSenderEmail(value string) string {
	value = strings.TrimSpace(value)
	if addr, err := mail.ParseAddress(value); err == nil {
		return addr.Address
	}
	return value
}

func messageAddressListContains(values []string, email string) bool {
	for _, value := range values {
		for _, addr := range parseAddressList(value) {
			if strings.EqualFold(addr, email) {
				return true
			}
		}
	}
	return false
}

func parseAddressList(value string) []string {
	addrs, err := mail.ParseAddressList(value)
	if err != nil {
		return []string{strings.TrimSpace(value)}
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.Address)
	}
	return out
}

func (a *App) statusError(message string) {
	a.status = message
	a.error = true
}

func (a *App) draw() {
	w, h := terminalSize()
	if w < 30 || h < 8 {
		fmt.Print("\x1b[2J\x1b[Hterminal too small")
		return
	}
	if a.lastW != w || a.lastH != h {
		fmt.Print("\x1b[2J")
		a.lastW = w
		a.lastH = h
	}
	filter := a.filter
	if filter == "" {
		filter = "all"
	} else if filter == "search" {
		filter = "search " + a.filterQuery
	}
	acct := "all accounts"
	if a.account > 0 && a.account-1 < len(a.accountsList) {
		acct = a.accountsList[a.account-1].Email
	}
	printStyledLine(0, 0, w, " murat | "+acct+" | "+strconv.Itoa(len(a.messages))+" messages | "+filter, styleHeader)
	status := a.status
	statusStyle := styleStatus
	if a.error {
		status = "ERROR: " + status
		statusStyle = styleError
		a.error = false
	}
	printStyledLine(h-2, 0, w, status, statusStyle)
	printStyledLine(h-1, 0, w, a.shortcuts(), styleHeader)
	if a.preview == nil {
		a.listArea = area{y: 1, x: 0, h: h - 3, w: w}
		a.bodyArea = area{y: 1, x: 0, h: 0, w: 0}
		a.drawList(1, 0, h-3, w)
		return
	}
	if a.split == "horizontal" {
		left := max(24, w/3)
		if left > w-20 {
			left = max(10, w/2)
		}
		a.listArea = area{y: 1, x: 0, h: h - 3, w: left}
		a.bodyArea = area{y: 1, x: left + 1, h: h - 3, w: w - left - 1}
		a.drawList(a.listArea.y, a.listArea.x, a.listArea.h, a.listArea.w)
		for row := 1; row < h-2; row++ {
			printSegment(row, left, 1, "|", styleDivider)
		}
		a.drawPreview(a.bodyArea.y, a.bodyArea.x, a.bodyArea.h, a.bodyArea.w)
		return
	}
	top := (h - 3) / 3
	if top < 3 {
		top = 3
	}
	a.listArea = area{y: 1, x: 0, h: top, w: w}
	a.bodyArea = area{y: 2 + top, x: 0, h: h - 3 - top - 1, w: w}
	a.drawList(a.listArea.y, a.listArea.x, a.listArea.h, a.listArea.w)
	printStyledLine(1+top, 0, w, strings.Repeat("-", w), styleDivider)
	a.drawPreview(a.bodyArea.y, a.bodyArea.x, a.bodyArea.h, a.bodyArea.w)
}

func (a *App) drawList(y, x, height, width int) {
	if height <= 0 {
		return
	}
	if len(a.messages) == 0 {
		printLine(y, x, width, "no mail; press "+a.keys.Sync+" to sync")
		return
	}
	if a.selected < a.scroll {
		a.scroll = a.selected
	}
	if a.selected >= a.scroll+height {
		a.scroll = a.selected - height + 1
	}
	for row := 0; row < height; row++ {
		if a.scroll+row >= len(a.messages) {
			printLine(y+row, x, width, "")
			continue
		}
		idx := a.scroll + row
		msg := a.messages[idx]
		spam := " "
		if msg.Spam {
			spam = "!"
		}
		line, spamX := tableRowText(msg, width)
		style := ""
		if idx == a.selected {
			style = styleSelected
		} else if msg.Spam {
			style = styleSpam
		} else if !msg.Read {
			style = styleUnread
		}
		printStyledLine(y+row, x, width, line, style)
		if idx != a.selected && msg.Spam {
			printSegment(y+row, x+spamX, max(0, width-spamX), spam, styleSpam)
		}
	}
}

func (a *App) drawPreview(y, x, height, width int) {
	if height <= 0 || a.preview == nil {
		return
	}
	content := a.previewBody
	if a.headerMode {
		content = a.previewHead
	}
	lines := previewLines(a.preview, content, width)
	if a.bodyScroll > len(lines)-1 {
		a.bodyScroll = max(0, len(lines)-1)
	}
	for row := 0; row < height; row++ {
		if a.bodyScroll+row >= len(lines) {
			printLine(y+row, x, width, "")
			continue
		}
		lineIndex := a.bodyScroll + row
		if sel, ok := a.selectionRangeForLine(lineIndex, lines[lineIndex]); ok {
			printSelectedPreviewLine(y+row, x, width, lines[lineIndex], sel)
		} else {
			printPreviewLine(y+row, x, width, lines[lineIndex])
		}
	}
}

func (a *App) selectionRangeForLine(index int, line previewLine) ([2]int, bool) {
	if a.selectStart == nil || a.selectEnd == nil {
		return [2]int{}, false
	}
	start, end := *a.selectStart, *a.selectEnd
	if pointLess(end, start) {
		start, end = end, start
	}
	if index < start.line || index > end.line {
		return [2]int{}, false
	}
	text := previewPlainText(line)
	left := 0
	right := displayLen(text)
	if index == start.line {
		left = min(start.col, right)
	}
	if index == end.line {
		right = min(end.col, right)
	}
	if right <= left {
		return [2]int{}, false
	}
	return [2]int{left, right}, true
}

type previewLine struct {
	label string
	value string
	text  string
	blank bool
}

func previewLines(msg *store.Message, content string, width int) []previewLine {
	rows := []previewLine{
		{label: "Subject", value: msg.Subject},
		{label: "From", value: msg.From},
		{label: "To", value: strings.Join(msg.To, ", ")},
		{label: "Date", value: firstNonEmpty(msg.ReceivedAt, msg.SentAt)},
		{label: "Tags", value: strings.Join(msg.DisplayTags(), ", ")},
		{blank: true},
	}
	if strings.TrimSpace(content) == "" {
		content = "(no body)"
	}
	for _, line := range wrap(content, max(10, width-1)) {
		rows = append(rows, previewLine{text: line})
	}
	return rows
}

func printPreviewLine(y, x, width int, line previewLine) {
	if line.blank {
		printLine(y, x, width, "")
		return
	}
	if line.label != "" {
		labelWidth := 9
		printLine(y, x, width, "")
		printSegment(y, x, min(labelWidth, width), line.label+":", styleLabel)
		if width > labelWidth {
			style := ""
			if line.label == "Tags" {
				style = styleStatus
			}
			printSegment(y, x+labelWidth, width-labelWidth, line.value, style)
		}
		return
	}
	printRichLine(y, x, width, line.text)
}

func printSelectedPreviewLine(y, x, width int, line previewLine, selected [2]int) {
	text := previewPlainText(line)
	printLine(y, x, width, "")
	left := selected[0]
	right := selected[1]
	printSegment(y, x, width, sliceRunes(text, 0, left), "")
	printSegment(y, x+left, max(0, width-left), sliceRunes(text, left, right), styleSelected)
	printSegment(y, x+right, max(0, width-right), sliceRunes(text, right, displayLen(text)), "")
}

func previewPlainText(line previewLine) string {
	if line.blank {
		return ""
	}
	if line.label != "" {
		return padRight(line.label+":", 9) + line.value
	}
	return richPlainText(line.text)
}

func previewLinks(line previewLine) []markdownLink {
	if line.blank || line.label != "" {
		return nil
	}
	return markdownLinks(line.text)
}

func (a *App) shortcuts() string {
	if a.actionScope == "mail" {
		return "mail: r reply  R reply-all  f forward  h headers  a attach  u unread  t trash  s spam  esc back"
	}
	if a.actionScope == "filter" {
		return "filter: s spam  e sent  D drafts  r read  u unread  d today  c clear  esc back"
	}
	if a.actionScope == "link" {
		return "link: enter open  c copy  esc cancel"
	}
	parts := []string{
		a.keys.Next + "/" + a.keys.Prev + " move",
		a.keys.Open + " open",
		a.keys.Actions + " actions",
		a.keys.Filter + " filters",
		"/ search",
		a.keys.Sync + " sync",
		a.keys.Compose + " compose",
	}
	if len(a.accountsList) > 1 {
		parts = append(parts, a.keys.CycleAccount+" acct")
	}
	parts = append(parts, a.keys.Quit+" quit")
	return strings.Join(parts, "  ")
}

func rawMode() error {
	cmd := exec.Command("stty", "raw", "-echo", "min", "0", "time", "1")
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func timedRawMode() error {
	cmd := exec.Command("stty", "raw", "-echo", "min", "0", "time", "1")
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func cookedMode() error {
	cmd := exec.Command("stty", "sane")
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func enableMouse() {
	fmt.Print("\x1b[?1000h\x1b[?1002h\x1b[?1006h")
}

func disableMouse() {
	fmt.Print("\x1b[?1006l\x1b[?1002l\x1b[?1000l")
}

func terminalSize() (int, int) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 100, 30
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 100, 30
	}
	h, _ := strconv.Atoi(fields[0])
	w, _ := strconv.Atoi(fields[1])
	if w == 0 || h == 0 {
		return 100, 30
	}
	return w, h
}

func readKey() (string, error) {
	var buf [8]byte
	n, err := os.Stdin.Read(buf[:1])
	if err == io.EOF || n == 0 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	b := buf[0]
	if b == 3 {
		return "ctrl-c", nil
	}
	if b == 8 || b == 127 {
		return "backspace", nil
	}
	if b == 13 || b == 10 {
		return "enter", nil
	}
	if b == 9 {
		return "tab", nil
	}
	if b == 27 {
		_ = timedRawMode()
		seq := readEscapeTail()
		_ = rawMode()
		if mouse := parseMouse(seq); mouse != "" {
			return mouse, nil
		}
		if len(seq) >= 2 && seq[0] == '[' {
			switch seq[1] {
			case 'A':
				return "up", nil
			case 'B':
				return "down", nil
			case '3':
				return "delete", nil
			case '5':
				return "pageup", nil
			case '6':
				return "pagedown", nil
			}
		}
		return "esc", nil
	}
	if b == ' ' {
		return "space", nil
	}
	return string([]byte{b}), nil
}

func readKeyBlocking() (string, error) {
	for {
		key, err := readKey()
		if err != nil || key != "" {
			return key, err
		}
	}
}

func readEscapeTail() string {
	var out strings.Builder
	var one [1]byte
	for out.Len() < 64 {
		n, err := os.Stdin.Read(one[:])
		if err != nil || n == 0 {
			break
		}
		out.WriteByte(one[0])
		if one[0] == 'M' || one[0] == 'm' || one[0] == '~' || (one[0] >= 'A' && one[0] <= 'D') {
			break
		}
	}
	return out.String()
}

func parseMouse(seq string) string {
	var button, x, y int
	var final rune
	if _, err := fmt.Sscanf(seq, "[<%d;%d;%d%c", &button, &x, &y, &final); err != nil {
		return ""
	}
	if final != 'M' && final != 'm' {
		return ""
	}
	state := "press"
	if final == 'm' {
		state = "release"
	}
	return fmt.Sprintf("mouse:%d:%d:%d:%s", button, max(0, x-1), max(0, y-1), state)
}

func printLine(y, x, width int, text string) {
	printStyledLine(y, x, width, text, "")
}

func printStyledLine(y, x, width int, text, style string) {
	if width <= 0 {
		return
	}
	text = padRight(clipRunes(text, width), width)
	if style == "" {
		fmt.Printf("\x1b[%d;%dH%s", y+1, x+1, text)
		return
	}
	fmt.Printf("\x1b[%d;%dH%s%s%s", y+1, x+1, style, text, styleReset)
}

func printSegment(y, x, width int, text, style string) {
	if width <= 0 {
		return
	}
	text = clipRunes(text, width)
	if style == "" {
		fmt.Printf("\x1b[%d;%dH%s", y+1, x+1, text)
		return
	}
	fmt.Printf("\x1b[%d;%dH%s%s%s", y+1, x+1, style, text, styleReset)
}

func printRichLine(y, x, width int, text string) {
	printLine(y, x, width, "")
	col := 0
	bold, italic, under := false, false, false
	for i := 0; i < len(text) && col < width; {
		if raw, _, label, ok := markdownLinkAt(text, i); ok {
			chunk := richPlainText(label)
			if displayLen(chunk) > width-col {
				chunk = clipRunes(chunk, width-col)
			}
			printSegment(y, x+col, width-col, chunk, styleLink)
			col += displayLen(chunk)
			i += len(raw)
			continue
		}
		if strings.HasPrefix(text[i:], "**") {
			bold = !bold
			i += 2
			continue
		}
		if strings.HasPrefix(text[i:], "__") {
			under = !under
			i += 2
			continue
		}
		if text[i] == '*' {
			italic = !italic
			i++
			continue
		}
		start := i
		for i < len(text) && !strings.HasPrefix(text[i:], "**") && !strings.HasPrefix(text[i:], "__") && text[i] != '*' {
			if _, _, _, ok := markdownLinkAt(text, i); ok {
				break
			}
			i++
		}
		chunk := text[start:i]
		if len(chunk) > width-col {
			chunk = chunk[:width-col]
		}
		style := richStyle(bold, italic, under)
		printSegment(y, x+col, width-col, chunk, style)
		col += len(chunk)
	}
}

type markdownLink struct {
	start int
	end   int
	url   string
}

func markdownLinks(text string) []markdownLink {
	out := []markdownLink{}
	for i := 0; i < len(text); {
		raw, url, label, ok := markdownLinkAt(text, i)
		if !ok {
			i++
			continue
		}
		start := displayLen(richPlainText(text[:i]))
		out = append(out, markdownLink{start: start, end: start + displayLen(richPlainText(label)), url: url})
		i += len(raw)
	}
	return out
}

func markdownLinkAt(text string, i int) (string, string, string, bool) {
	if i >= len(text) || text[i] != '[' {
		return "", "", "", false
	}
	labelEnd := strings.Index(text[i:], "](")
	if labelEnd <= 1 {
		return "", "", "", false
	}
	labelEnd += i
	urlStart := labelEnd + 2
	urlEnd := strings.IndexByte(text[urlStart:], ')')
	if urlEnd <= 0 {
		return "", "", "", false
	}
	urlEnd += urlStart
	url := strings.TrimSpace(text[urlStart:urlEnd])
	if url == "" || (!strings.HasPrefix(strings.ToLower(url), "http://") && !strings.HasPrefix(strings.ToLower(url), "https://") && !strings.HasPrefix(strings.ToLower(url), "mailto:")) {
		return "", "", "", false
	}
	return text[i : urlEnd+1], url, text[i+1 : labelEnd], true
}

func richPlainText(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if raw, _, label, ok := markdownLinkAt(text, i); ok {
			out.WriteString(richPlainText(label))
			i += len(raw)
			continue
		}
		if strings.HasPrefix(text[i:], "**") || strings.HasPrefix(text[i:], "__") {
			i += 2
			continue
		}
		if text[i] == '*' {
			i++
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}

func copyToClipboard(text string) string {
	commands := [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return "copied selection"
		}
	}
	writeOSC52Clipboard(text)
	return "copied selection"
}

func writeOSC52Clipboard(text string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	seq := "\x1b]52;c;" + encoded + "\x07"
	if os.Getenv("TMUX") != "" {
		seq = "\x1bPtmux;\x1b" + seq + "\x1b\\"
	}
	fmt.Print(seq)
}

func richStyle(bold, italic, under bool) string {
	parts := []string{}
	if bold {
		parts = append(parts, styleBold)
	}
	if italic {
		parts = append(parts, styleItalic)
	}
	if under {
		parts = append(parts, styleUnder)
	}
	return strings.Join(parts, "")
}

func (a *App) runEditor(path string) error {
	_ = cookedMode()
	err := compose.RunEditorWith(path, a.editor)
	_ = rawMode()
	fmt.Print("\x1b[?1049h\x1b[?25l")
	enableMouse()
	return err
}

func (a *App) promptLine(label string) (string, error) {
	value, _, err := a.promptInput(label+": ", nil)
	return value, err
}

func (a *App) promptPath(label, defaultValue string) (string, bool, error) {
	return a.promptInput(label+" ["+defaultValue+"]: ", func(value string) string {
		return completeDirectoryPath(value, filepath.Dir(defaultValue))
	})
}

func (a *App) promptFile(label, fallbackDir string) (string, bool, error) {
	return a.promptInput(label+": ", func(value string) string {
		return completeFilePath(value, fallbackDir)
	})
}

func (a *App) promptInput(prompt string, complete func(string) string) (string, bool, error) {
	w, h := terminalSize()
	var value strings.Builder
	fmt.Print("\x1b[?25h")
	defer func() {
		fmt.Print("\x1b[?25l")
		a.dirty = true
	}()
	for {
		printStyledLine(h-2, 0, w, prompt+value.String(), styleStatus)
		fmt.Printf("\x1b[%d;%dH", h-1, min(w, len(prompt)+displayLen(value.String()))+1)
		key, err := readKeyBlocking()
		if err != nil {
			return "", false, err
		}
		switch key {
		case "enter":
			return value.String(), false, nil
		case "esc":
			return "", true, nil
		case "backspace", "delete":
			removeLastRune(&value)
		case "tab":
			if complete != nil {
				completed := complete(value.String())
				value.Reset()
				value.WriteString(completed)
			}
		case "ctrl-c":
			return "", true, nil
		case "space":
			value.WriteByte(' ')
		default:
			if len(key) == 1 && key[0] >= 32 && key[0] != 127 {
				value.WriteString(key)
			}
		}
	}
}

func completeDirectoryPath(input, fallbackDir string) string {
	return completePath(input, fallbackDir, false)
}

func completeFilePath(input, fallbackDir string) string {
	return completePath(input, fallbackDir, true)
}

func completePath(input, fallbackDir string, includeFiles bool) string {
	if input == "" {
		return withTrailingSeparator(fallbackDir)
	}
	dirPart, prefix := filepath.Split(input)
	scan := dirPart
	if scan == "" {
		scan = "."
	}
	entries, err := os.ReadDir(userdirs.Expand(scan))
	if err != nil {
		return input
	}
	type match struct {
		name  string
		isDir bool
	}
	matches := []match{}
	for _, entry := range entries {
		if !entry.IsDir() && !includeFiles {
			continue
		}
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		matches = append(matches, match{name: entry.Name(), isDir: entry.IsDir()})
	}
	if len(matches) == 0 {
		return input
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].name < matches[j].name })
	if len(matches) == 1 {
		path := dirPart + matches[0].name
		if matches[0].isDir {
			return withTrailingSeparator(path)
		}
		return path
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.name)
	}
	common := commonPrefix(names)
	if len(common) > len(prefix) {
		return dirPart + common
	}
	return input
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func withTrailingSeparator(path string) string {
	if path == "" || strings.HasSuffix(path, string(os.PathSeparator)) {
		return path
	}
	return path + string(os.PathSeparator)
}

func removeLastRune(value *strings.Builder) {
	text := value.String()
	if text == "" {
		return
	}
	runes := []rune(text)
	value.Reset()
	value.WriteString(string(runes[:len(runes)-1]))
}

func quote(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

func replyAttribution(msg *store.Message) string {
	return "On " + firstNonEmpty(msg.ReceivedAt, msg.SentAt) + ", " + msg.From + " wrote:"
}

func prefixedSubject(prefix, subject string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), strings.ToLower(prefix)) {
		return subject
	}
	return prefix + " " + subject
}

func formatDraftPreview(draft protocol.Draft) string {
	lines := []string{
		"From: " + draft.From,
		"To: " + draft.To,
		"Cc: " + draft.Cc,
		"Bcc: " + draft.Bcc,
		"Subject: " + draft.Subject,
	}
	if len(draft.Attachments) > 0 {
		lines = append(lines, "Attachments:")
		for _, attachment := range draft.Attachments {
			lines = append(lines, "  - "+draftAttachmentSummary(attachment))
		}
	}
	return strings.Join(lines, "\n") + "\n\n" + draft.Body
}

func draftAttachmentSummary(attachment protocol.Attachment) string {
	name := strings.TrimSpace(attachment.Filename)
	if name == "" {
		name = "attachment"
	}
	contentType := strings.TrimSpace(attachment.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return fmt.Sprintf("%s (%s, %dB)", name, contentType, len(attachment.Data))
}

func composePGPLine(draft protocol.Draft, availability pgp.Availability, menu bool) string {
	if menu {
		return composePGPMenuLine(draft, availability)
	}
	return composePGPSummary(draft)
}

func composePGPSummary(draft protocol.Draft) string {
	options := pgpSet(draft.PGP)
	enabled := []string{}
	for _, item := range []string{"encrypt", "sign", "self-encrypt", "attach-pubkey"} {
		if options[item] {
			enabled = append(enabled, item)
		}
	}
	if len(enabled) == 0 {
		return "PGP: none"
	}
	return "PGP: " + strings.Join(enabled, ", ")
}

func composePGPMenuLine(draft protocol.Draft, availability pgp.Availability) string {
	options := pgpSet(draft.PGP)
	parts := []string{}
	if availability.Sign {
		parts = append(parts, "s sign="+yesNo(options["sign"]))
	}
	if availability.AttachPublicKey {
		parts = append(parts, "a pubkey="+yesNo(options["attach-pubkey"]))
	}
	if availability.Encrypt {
		parts = append(parts, "e encrypt="+yesNo(options["encrypt"]))
	}
	if availability.SelfEncrypt {
		parts = append(parts, "E self="+yesNo(options["self-encrypt"]))
	}
	if len(parts) == 0 {
		return "PGP: no available options"
	}
	return "PGP menu: " + strings.Join(parts, "  ")
}

func composeShortcuts(draft protocol.Draft, availability pgp.Availability, menu bool) string {
	if menu {
		parts := []string{"pgp:"}
		if availability.Sign {
			parts = append(parts, "s sign")
		}
		if availability.AttachPublicKey {
			parts = append(parts, "a attach pubkey")
		}
		if availability.Encrypt {
			parts = append(parts, "e encrypt")
		}
		if availability.SelfEncrypt {
			parts = append(parts, "E self encrypt")
		}
		parts = append(parts, "esc back")
		return strings.Join(parts, "  ")
	}
	parts := []string{"enter/s send", "e edit", "a attach", "d draft"}
	if len(draft.Attachments) > 0 {
		parts = append(parts, "A detach")
	}
	if anyPGPAvailable(availability) {
		parts = append(parts, "g pgp")
	}
	parts = append(parts, "x cancel")
	return strings.Join(parts, "  ")
}

func handleComposePGPKey(key string, draft *protocol.Draft, availability pgp.Availability) bool {
	switch key {
	case "s":
		if availability.Sign {
			togglePGP(draft, "sign")
		}
		return true
	case "a":
		if availability.AttachPublicKey {
			togglePGP(draft, "attach-pubkey")
		}
		return true
	case "e":
		if availability.Encrypt {
			togglePGP(draft, "encrypt")
		}
		return true
	case "E":
		if availability.SelfEncrypt {
			togglePGP(draft, "self-encrypt")
		}
		return true
	case "g", "q", "esc":
		return true
	default:
		return false
	}
}

func anyPGPAvailable(availability pgp.Availability) bool {
	return availability.Sign || availability.AttachPublicKey || availability.Encrypt || availability.SelfEncrypt
}

func togglePGP(draft *protocol.Draft, option string) {
	options := pgpSet(draft.PGP)
	options[option] = !options[option]
	if option == "self-encrypt" && options[option] {
		options["encrypt"] = true
	}
	if option == "encrypt" && !options[option] {
		delete(options, "self-encrypt")
	}
	ordered := []string{}
	for _, item := range []string{"encrypt", "sign", "self-encrypt", "attach-pubkey"} {
		if options[item] {
			ordered = append(ordered, item)
		}
	}
	draft.PGP = strings.Join(ordered, ",")
}

func restrictPGP(draft *protocol.Draft, availability pgp.Availability) {
	options := pgpSet(draft.PGP)
	if !availability.Sign {
		delete(options, "sign")
	}
	if !availability.Encrypt {
		delete(options, "encrypt")
	}
	if !availability.SelfEncrypt {
		delete(options, "self-encrypt")
	}
	if !options["encrypt"] {
		delete(options, "self-encrypt")
	}
	if !availability.AttachPublicKey {
		delete(options, "attach-pubkey")
	}
	ordered := []string{}
	for _, item := range []string{"encrypt", "sign", "self-encrypt", "attach-pubkey"} {
		if options[item] {
			ordered = append(ordered, item)
		}
	}
	draft.PGP = strings.Join(ordered, ",")
}

func pgpSet(value string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\t' }) {
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func pgpBodyProtectionEnabled(value string) bool {
	options := pgpSet(value)
	return options["encrypt"] || options["encrypted"] ||
		options["sign"] || options["signed"] ||
		options["self-encrypt"] || options["selfencrypt"] || options["self"]
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func tableRowText(msg *store.Message, width int) (string, int) {
	folderWidth := 10
	folder := padRight(shorten(msg.FolderColumn(), folderWidth), folderWidth)
	marker := " "
	if !msg.Read {
		marker = "*"
	}
	attach := " "
	if msg.HasAttachment {
		attach = "@"
	}
	spam := " "
	if msg.Spam {
		spam = "!"
	}
	date := shortDate(firstNonEmpty(msg.ReceivedAt, msg.SentAt))
	sender := msg.From
	subject := msg.Subject
	prefix := folder + " " + marker + attach + spam + " "
	spamX := folderWidth + 1 + 2
	if width < 72 {
		senderWidth := max(10, min(20, width/3))
		line := prefix + padRight(shorten(sender, senderWidth), senderWidth) + " " + subject
		return line, spamX
	}
	senderWidth := 22
	line := prefix + padRight(date, 10) + " " + padRight(shorten(sender, senderWidth), senderWidth) + " " + subject
	return line, spamX
}

func firstTags(values []string, count int) []string {
	out := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, value)
		if len(out) == count {
			break
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isTextAttachment(att store.Attachment) bool {
	name := strings.ToLower(att.Filename)
	kind := strings.ToLower(att.ContentType)
	return strings.HasPrefix(kind, "text/") || strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".asc")
}

func decodeBytesForUI(data []byte, contentType string) string {
	_, params, _ := strings.Cut(contentType, ";")
	charset := ""
	for _, item := range strings.Split(params, ";") {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "charset") {
			charset = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	charset = strings.ToLower(charset)
	if charset == "iso-8859-1" || charset == "latin1" || charset == "latin-1" {
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes)
	}
	return string(data)
}

func safeName(value string) string {
	value = strings.TrimSpace(filepath.Base(value))
	if value == "" || value == "." || value == string(os.PathSeparator) {
		return "attachment"
	}
	return value
}

func replyAddresses(msg *store.Message, self string, replyAll bool) (string, string) {
	if !replyAll {
		return msg.From, ""
	}
	to := []string{msg.From}
	cc := []string{}
	seen := map[string]bool{addressKey(self): true, addressKey(msg.From): true}
	for _, value := range append(msg.To, msg.Cc...) {
		key := addressKey(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		cc = append(cc, value)
	}
	return strings.Join(to, ", "), strings.Join(cc, ", ")
}

func addressKey(value string) string {
	addr, err := mail.ParseAddress(value)
	if err == nil {
		return strings.ToLower(addr.Address)
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func wrap(text string, width int) []string {
	if width < 10 {
		width = 10
	}
	out := []string{}
	for _, raw := range strings.Split(text, "\n") {
		line := []rune(raw)
		if len(line) == 0 {
			out = append(out, "")
			continue
		}
		for len(line) > width {
			out = append(out, string(line[:width]))
			line = line[width:]
		}
		out = append(out, string(line))
	}
	return out
}

func shortDate(value string) string {
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

func shorten(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayLen(value) <= width {
		return value
	}
	if width <= 3 {
		return clipRunes(value, width)
	}
	return clipRunes(value, width-3) + "..."
}

func clipRunes(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:width])
}

func padRight(value string, width int) string {
	length := displayLen(value)
	if length >= width {
		return clipRunes(value, width)
	}
	return value + strings.Repeat(" ", width-length)
}

func displayLen(value string) int { return len([]rune(value)) }

func sliceRunes(value string, start, end int) string {
	runes := []rune(value)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start > end {
		start = end
	}
	return string(runes[start:end])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
