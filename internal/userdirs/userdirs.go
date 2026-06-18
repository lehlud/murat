package userdirs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Downloads returns the user's download directory, preferring XDG user-dirs.
func Downloads() string {
	home := homeDir()
	if dir := existingDir(os.Getenv("XDG_DOWNLOAD_DIR")); dir != "" {
		return dir
	}
	if dir := xdgDownloadsDir(home); dir != "" {
		return dir
	}
	if home == "" {
		return "."
	}
	return filepath.Join(home, "Downloads")
}

func Cache() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); dir != "" {
		return Expand(dir)
	}
	if home := homeDir(); home != "" {
		return filepath.Join(home, ".cache")
	}
	return "."
}

// Expand applies shell-like home and environment expansion for user paths.
func Expand(path string) string {
	if path == "~" {
		return homeDir()
	}
	if strings.HasPrefix(path, "~/") {
		if home := homeDir(); home != "" {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return os.ExpandEnv(path)
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return os.Getenv("HOME")
}

func xdgDownloadsDir(home string) string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" && home != "" {
		configHome = filepath.Join(home, ".config")
	}
	if configHome == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(Expand(configHome), "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "XDG_DOWNLOAD_DIR=") {
			continue
		}
		_, value, _ := strings.Cut(line, "=")
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `"`)
		}
		value = strings.ReplaceAll(value, "$HOME", home)
		value = strings.ReplaceAll(value, "${HOME}", home)
		return existingDir(value)
	}
	return ""
}

func existingDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = Expand(path)
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() {
		return ""
	}
	return path
}
