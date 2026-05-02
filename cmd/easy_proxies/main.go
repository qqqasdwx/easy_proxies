package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"easy_proxies/internal/app"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"

	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	var databasePath string
	flag.StringVar(&databasePath, "database", "data/data.db", "path to SQLite database")
	flag.Parse()

	st, err := store.Open(databasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	cfg, err := config.RuntimeFromStore(context.Background(), st)
	if err != nil {
		_ = st.Close()
		log.Fatalf("load runtime config: %v", err)
	}
	cfg.DatabasePath = databasePath

	// Setup logging based on config
	setupLogging(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.RunWithStore(ctx, cfg, st); err != nil {
		fmt.Fprintf(os.Stderr, "proxy pool exited with error: %v\n", err)
		os.Exit(1)
	}
}

func setupLogging(cfg *config.Config) {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Always include the in-memory ring buffer for dashboard console
	writers := []io.Writer{os.Stdout, monitor.LogWriter()}

	if cfg.Log.Output == "file" {
		// Ensure log directory exists
		logDir := filepath.Dir(cfg.Log.File)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			log.Printf("\u26a0\ufe0f Failed to create log dir %s: %v, falling back to stdout", logDir, err)
		} else {
			lj := &lumberjack.Logger{
				Filename:   cfg.Log.File,
				MaxSize:    cfg.Log.MaxSize, // MB
				MaxBackups: cfg.Log.MaxBackups,
				MaxAge:     cfg.Log.MaxAge, // days
				Compress:   cfg.Log.Compress,
			}
			writers = append(writers, lj)
			log.Printf("\u2705 Log rotation enabled: file=%s, maxSize=%dMB, maxBackups=%d, maxAge=%dd",
				cfg.Log.File, cfg.Log.MaxSize, cfg.Log.MaxBackups, cfg.Log.MaxAge)
		}
	}

	log.SetOutput(io.MultiWriter(writers...))
}
