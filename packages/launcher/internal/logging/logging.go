// Package logging 初始化带滚动的文件日志：单文件 5 MB，保留 3 份备份。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	maxLogSize = 5 << 20
	maxBackups = 3
)

var (
	mu          sync.Mutex
	currentPath string
	writer      *rotatingWriter
)

// Setup 初始化 slog 默认 logger；重复调用返回同一个路径。
func Setup() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if writer != nil {
		return currentPath, nil
	}

	path, err := defaultPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	rotating, err := newRotatingWriter(path, maxLogSize, maxBackups)
	if err != nil {
		return "", err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, rotating), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
	currentPath = path
	writer = rotating
	return path, nil
}

// Path 返回当前日志文件路径；未初始化时返回平台默认路径。
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	if currentPath != "" {
		return currentPath
	}
	path, err := defaultPath()
	if err != nil {
		return ""
	}
	return path
}

func defaultPath() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Logs", "VaKaoHub", "launcher.log"), nil
	case "windows":
		base := os.Getenv("LocalAppData")
		if base == "" {
			var err error
			base, err = os.UserConfigDir()
			if err != nil {
				return "", err
			}
		}
		return filepath.Join(base, "VaKaoHub", "logs", "launcher.log"), nil
	default:
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "state")
		}
		return filepath.Join(base, "VaKaoHub", "launcher.log"), nil
	}
}

type rotatingWriter struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
	file    *os.File
	size    int64
}

func newRotatingWriter(path string, max int64, backups int) (*rotatingWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &rotatingWriter{path: path, max: max, backups: backups, file: file, size: info.Size()}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.max {
		w.rotateLocked()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotateLocked() {
	if w.file != nil {
		_ = w.file.Close()
	}
	oldest := fmt.Sprintf("%s.%d", w.path, w.backups)
	_ = os.Remove(oldest)
	for index := w.backups - 1; index >= 1; index-- {
		from := fmt.Sprintf("%s.%d", w.path, index)
		to := fmt.Sprintf("%s.%d", w.path, index+1)
		_ = os.Rename(from, to)
	}
	_ = os.Rename(w.path, w.path+".1")

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		w.file = nil
		w.size = 0
		return
	}
	w.file = file
	w.size = 0
}
