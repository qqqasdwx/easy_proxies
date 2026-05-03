package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"easy_proxies/internal/config"

	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	mu          sync.Mutex
	currentFile *lumberjack.Logger
)

// Configure applies process-wide logging output and rotation settings.
func Configure(cfg config.LogConfig, extraWriters ...io.Writer) error {
	mu.Lock()
	defer mu.Unlock()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	writers := []io.Writer{os.Stdout}
	writers = append(writers, extraWriters...)

	var nextFile *lumberjack.Logger
	output := strings.ToLower(strings.TrimSpace(cfg.Output))
	if output == "file" {
		if strings.TrimSpace(cfg.File) == "" {
			return fmt.Errorf("log file path is empty")
		}
		logDir := filepath.Dir(cfg.File)
		if logDir != "" && logDir != "." {
			if err := os.MkdirAll(logDir, 0o755); err != nil {
				return fmt.Errorf("create log directory %s: %w", logDir, err)
			}
		}
		nextFile = &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
		writers = append(writers, nextFile)
	}

	oldFile := currentFile
	currentFile = nextFile
	log.SetOutput(io.MultiWriter(writers...))
	if oldFile != nil {
		_ = oldFile.Close()
	}

	if nextFile != nil {
		log.Printf("✅ Log rotation enabled: file=%s, maxSize=%dMB, maxBackups=%d, maxAge=%dd",
			cfg.File, cfg.MaxSize, cfg.MaxBackups, cfg.MaxAge)
	}
	return nil
}
