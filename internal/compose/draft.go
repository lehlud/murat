package compose

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lehnert.dev/murat/internal/protocol"
)

func Edit(draft protocol.Draft) (protocol.Draft, error) {
	return EditWithEditor(draft, "")
}

func EditWithEditor(draft protocol.Draft, editor string) (protocol.Draft, error) {
	path, err := WriteDraftFile(draft)
	if err != nil {
		return protocol.Draft{}, err
	}
	defer os.Remove(path)
	if err := RunEditorWith(path, editor); err != nil {
		return protocol.Draft{}, err
	}
	return ReadDraftFile(path)
}

func WriteDraftFile(draft protocol.Draft) (string, error) {
	file, err := os.CreateTemp("", "murat-compose-*.txt")
	if err != nil {
		return "", err
	}
	defer file.Close()
	if strings.TrimSpace(draft.PGP) != "" {
		_, err = fmt.Fprintf(file, "From: %s\nTo: %s\nCc: %s\nBcc: %s\nSubject: %s\nPGP: %s\n\n%s", draft.From, draft.To, draft.Cc, draft.Bcc, draft.Subject, draft.PGP, draft.Body)
		return file.Name(), err
	}
	_, err = fmt.Fprintf(file, "From: %s\nTo: %s\nCc: %s\nBcc: %s\nSubject: %s\n\n%s", draft.From, draft.To, draft.Cc, draft.Bcc, draft.Subject, draft.Body)
	return file.Name(), err
}

func ReadDraftFile(path string) (protocol.Draft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return protocol.Draft{}, err
	}
	lines := strings.Split(string(data), "\n")
	draft := protocol.Draft{}
	bodyAt := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			bodyAt = i + 1
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "from":
			draft.From = strings.TrimSpace(value)
		case "to":
			draft.To = strings.TrimSpace(value)
		case "cc":
			draft.Cc = strings.TrimSpace(value)
		case "bcc":
			draft.Bcc = strings.TrimSpace(value)
		case "subject":
			draft.Subject = strings.TrimSpace(value)
		case "pgp":
			draft.PGP = strings.TrimSpace(value)
		}
	}
	if bodyAt < len(lines) {
		draft.Body = strings.Join(lines[bodyAt:], "\n")
	}
	return draft, nil
}

func RunEditor(path string) error {
	return RunEditorWith(path, "")
}

func RunEditorWith(path, editor string) error {
	editor = strings.TrimSpace(editor)
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command("sh", "-c", editor+" \"$1\"", "murat-editor", path)
	env, cleanup := editorEnv()
	defer cleanup()
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func editorEnv() ([]string, func()) {
	env := os.Environ()
	exe, err := os.Executable()
	if err != nil {
		return env, func() {}
	}
	dir, cleanup := muratPathDir(exe)
	path := os.Getenv("PATH")
	for i, item := range env {
		if strings.HasPrefix(item, "PATH=") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(item, "PATH=")
			return env, cleanup
		}
	}
	return append(env, "PATH="+dir+string(os.PathListSeparator)+path), cleanup
}

func muratPathDir(exe string) (string, func()) {
	if filepath.Base(exe) == "murat" {
		return filepath.Dir(exe), func() {}
	}
	dir, err := os.MkdirTemp("", "murat-editor-path-*")
	if err != nil {
		return filepath.Dir(exe), func() {}
	}
	link := filepath.Join(dir, "murat")
	if err := os.Symlink(exe, link); err != nil {
		_ = os.RemoveAll(dir)
		return filepath.Dir(exe), func() {}
	}
	return dir, func() { _ = os.RemoveAll(dir) }
}

func EmptyRecipient(draft protocol.Draft) bool {
	return strings.TrimSpace(draft.To+draft.Cc+draft.Bcc) == ""
}
