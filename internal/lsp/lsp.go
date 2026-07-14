package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"lehnert.dev/murat/internal/store"
)

type Server struct {
	r     *bufio.Reader
	w     io.Writer
	docs  map[string]string
	items []addressItem
}

type addressItem struct {
	Label string
	Email string
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func Run(r io.Reader, w io.Writer, addresses []store.KnownAddress) error {
	server := &Server{r: bufio.NewReader(r), w: w, docs: map[string]string{}, items: completionItems(addresses)}
	return server.run()
}

func completionItems(addresses []store.KnownAddress) []addressItem {
	seen := map[string]bool{}
	items := []addressItem{}
	for _, addr := range addresses {
		email := strings.TrimSpace(addr.Email)
		if email == "" || seen[strings.ToLower(email)] {
			continue
		}
		seen[strings.ToLower(email)] = true
		items = append(items, addressItem{Label: addr.String(), Email: email})
	}
	return items
}

func (s *Server) run() error {
	for {
		msg, err := s.readMessage()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch msg.Method {
		case "initialize":
			if err := s.respond(msg.ID, map[string]any{"capabilities": map[string]any{"textDocumentSync": 1, "completionProvider": map[string]any{"triggerCharacters": []string{"@", ",", " "}}}}); err != nil {
				return err
			}
		case "initialized":
		case "shutdown":
			if err := s.respond(msg.ID, nil); err != nil {
				return err
			}
		case "exit":
			return nil
		case "textDocument/didOpen":
			s.didOpen(msg.Params)
		case "textDocument/didChange":
			s.didChange(msg.Params)
		case "textDocument/completion":
			if err := s.respond(msg.ID, s.completion(msg.Params)); err != nil {
				return err
			}
		default:
			if len(msg.ID) > 0 {
				if err := s.respond(msg.ID, nil); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Server) readMessage() (rpcMessage, error) {
	length := -1
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return rpcMessage{}, err
		}
		length = parsed
	}
	if length < 0 {
		return rpcMessage{}, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(s.r, body); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return rpcMessage{}, err
	}
	return msg, nil
}

func (s *Server) respond(id json.RawMessage, result any) error {
	if len(id) == 0 {
		return nil
	}
	body, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func (s *Server) didOpen(params json.RawMessage) {
	var value struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &value) == nil {
		s.docs[value.TextDocument.URI] = value.TextDocument.Text
	}
}

func (s *Server) didChange(params json.RawMessage) {
	var value struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if json.Unmarshal(params, &value) != nil || len(value.ContentChanges) == 0 {
		return
	}
	s.docs[value.TextDocument.URI] = value.ContentChanges[len(value.ContentChanges)-1].Text
}

func (s *Server) completion(params json.RawMessage) map[string]any {
	var value struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
	}
	if json.Unmarshal(params, &value) != nil {
		return completionList(nil)
	}
	items := completeAddresses(s.docs[value.TextDocument.URI], value.Position.Line, value.Position.Character, s.items)
	return completionList(items)
}

func completionList(items []map[string]any) map[string]any {
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"isIncomplete": false, "items": items}
}

func completeAddresses(text string, lineNumber, character int, addresses []addressItem) []map[string]any {
	line, ok := lineAt(text, lineNumber)
	if !ok {
		return nil
	}
	start, prefix, ok := completionContext(line, character)
	if !ok {
		return nil
	}
	prefix = strings.ToLower(prefix)
	items := []map[string]any{}
	for _, addr := range addresses {
		label := addr.Label
		if prefix != "" && !strings.Contains(strings.ToLower(label), prefix) && !strings.Contains(strings.ToLower(addr.Email), prefix) {
			continue
		}
		items = append(items, map[string]any{
			"label":      label,
			"kind":       6,
			"detail":     addr.Email,
			"insertText": label,
			"textEdit": map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": lineNumber, "character": start},
					"end":   map[string]any{"line": lineNumber, "character": character},
				},
				"newText": label,
			},
		})
	}
	return items
}

func lineAt(text string, lineNumber int) (string, bool) {
	if lineNumber < 0 {
		return "", false
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if lineNumber >= len(lines) {
		return "", false
	}
	return lines[lineNumber], true
}

func completionContext(line string, character int) (int, string, bool) {
	colon := strings.Index(line, ":")
	if colon < 0 {
		return 0, "", false
	}
	field := strings.ToLower(strings.TrimSpace(line[:colon]))
	switch field {
	case "from", "to", "cc", "bcc":
	default:
		return 0, "", false
	}
	pos := byteIndexForCharacter(line, character)
	if pos < colon+1 {
		return character, "", true
	}
	start := colon + 1
	if comma := strings.LastIndex(line[:pos], ","); comma >= start {
		start = comma + 1
	}
	for start < pos && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	return characterForByte(line, start), strings.TrimSpace(line[start:pos]), true
}

func byteIndexForCharacter(line string, character int) int {
	if character <= 0 {
		return 0
	}
	count := 0
	for i := range line {
		if count == character {
			return i
		}
		count++
	}
	return len(line)
}

func characterForByte(line string, index int) int {
	if index <= 0 {
		return 0
	}
	if index > len(line) {
		index = len(line)
	}
	return len([]rune(line[:index]))
}

func EncodeRequest(method string, id int, params any) []byte {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	var out bytes.Buffer
	fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n", len(body))
	out.Write(body)
	return out.Bytes()
}
