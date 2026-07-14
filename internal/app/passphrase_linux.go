//go:build linux

package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

func readBackupPassphrase() ([]byte, error) {
	passphrase, err := readBackupPassphrasePrompt("Backup passphrase: ")
	if err != nil {
		return nil, err
	}
	if err := validBackupPassphrase(passphrase); err != nil {
		clearBytes(passphrase)
		return nil, err
	}
	return passphrase, nil
}

func readNewBackupPassphrase() ([]byte, error) {
	first, err := readBackupPassphrasePrompt("New backup passphrase: ")
	if err != nil {
		return nil, err
	}
	if err := validBackupPassphrase(first); err != nil {
		clearBytes(first)
		return nil, err
	}
	second, err := readBackupPassphrasePrompt("Repeat backup passphrase: ")
	if err != nil {
		clearBytes(first)
		return nil, err
	}
	defer clearBytes(second)
	if !bytes.Equal(first, second) {
		clearBytes(first)
		return nil, fmt.Errorf("backup passphrases do not match")
	}
	return first, nil
}

func readSSHKeyPassphrase(path string) ([]byte, error) {
	return readBackupPassphrasePrompt("SSH key passphrase for " + path + ": ")
}

func readBackupPassphrasePrompt(prompt string) ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	defer tty.Close()

	if _, err := io.WriteString(tty, prompt); err != nil {
		return nil, err
	}
	state, err := getTerminalState(tty.Fd())
	if err != nil {
		return nil, err
	}
	noEcho := state
	noEcho.Lflag &^= syscall.ECHO
	if err := setTerminalState(tty.Fd(), noEcho); err != nil {
		return nil, err
	}
	defer setTerminalState(tty.Fd(), state)

	line, err := bufio.NewReader(tty).ReadBytes('\n')
	_, _ = io.WriteString(tty, "\n")
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

func getTerminalState(fd uintptr) (syscall.Termios, error) {
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&state)))
	if errno != 0 {
		return state, errno
	}
	return state, nil
}

func setTerminalState(fd uintptr, state syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&state)))
	if errno != 0 {
		return errno
	}
	return nil
}
