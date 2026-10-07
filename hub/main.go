// Command hackbox-hub is the hack-box session hub. See DEPLOYMENT.md and
// openapi.yaml next to this file.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0gfoundation/0g-hack-box/hub/internal/hub"
)

//go:embed api_test.html
var apiTestPage []byte

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listen := flag.String("listen", env("HACKBOX_HUB_LISTEN", "0.0.0.0:8210"), "listen address (env HACKBOX_HUB_LISTEN)")
	data := flag.String("data", env("HACKBOX_HUB_DATA", "./data"), "data dir for hub.db and archives/ (env HACKBOX_HUB_DATA)")
	config := flag.String("config", env("HACKBOX_HUB_CONFIG", "./config.json"), "JSON config file (env HACKBOX_HUB_CONFIG)")
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags)
	cfg, err := hub.LoadConfig(*config)
	if err != nil {
		logger.Fatalf("config: %v", err)
	}
	// The faucet key may come from the environment instead of the config file.
	if k := os.Getenv("FAUCET_API_KEY"); k != "" && cfg.Faucet.APIKey == "" {
		cfg.Faucet.APIKey = k
	}
	if err := os.MkdirAll(*data, 0o700); err != nil {
		logger.Fatalf("data dir: %v", err)
	}
	srv, err := hub.New(hub.Options{Config: cfg, DataDir: *data, Logger: logger, APITestPage: apiTestPage})
	if err != nil {
		logger.Fatalf("start: %v", err)
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go srv.RunWorker(ctx)

	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		logger.Printf("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	logger.Printf("hackbox-hub listening on %s, data %s, %d boxes, public %s", *listen, *data, len(cfg.Boxes), cfg.PublicBaseURL)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("listen: %v", err)
	}
	<-done // let in-flight requests finish before the database closes
}
