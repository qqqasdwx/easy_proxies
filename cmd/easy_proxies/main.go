package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"easy_proxies/internal/app"
	"easy_proxies/internal/config"
	"easy_proxies/internal/logging"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
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
	if err := logging.Configure(cfg.Log, monitor.LogWriter()); err != nil {
		log.Printf("⚠️  Failed to configure logging: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.RunWithStore(ctx, cfg, st); err != nil {
		fmt.Fprintf(os.Stderr, "proxy pool exited with error: %v\n", err)
		os.Exit(1)
	}
}
