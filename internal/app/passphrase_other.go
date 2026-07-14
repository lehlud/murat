//go:build !linux

package app

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

func readBackupPassphrase() ([]byte, error) {
	return nil, fmt.Errorf("backup passphrase prompt is not supported on this platform")
}

func readNewBackupPassphrase() ([]byte, error) {
	return nil, fmt.Errorf("backup passphrase prompt is not supported on this platform")
}

func readSSHKeyPassphrase(path string) ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	defer tty.Close()

	if _, err := fmt.Fprint(tty, "SSH key passphrase for "+path+": "); err != nil {
		return nil, err
	}
	if err := terminalEcho(tty, false); err != nil {
		return nil, err
	}
	defer func() {
		_ = terminalEcho(tty, true)
		_, _ = fmt.Fprintln(tty)
	}()

	line, err := bufio.NewReader(tty).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

func terminalEcho(tty *os.File, enabled bool) error {
	arg := "-echo"
	if enabled {
		arg = "echo"
	}
	cmd := exec.Command("stty", arg)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	return cmd.Run()
}
