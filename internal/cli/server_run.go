package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cresto/internal/config"
	"cresto/internal/groww"
	"cresto/internal/kite"
	"cresto/internal/llm"
	"cresto/internal/pdfstore"
	"cresto/internal/store"
	"cresto/internal/web"
)

// runServer configures and starts the HTTP server, blocking until a
// shutdown signal is received. In silent mode (daemon), logs are
// redirected to a file in the data directory and startup messages
// are suppressed.
func runServer(cfg config.Config, addr string, silent bool) error {
	if silent {
		logPath := filepath.Join(cfg.DataDir, "server.log")
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(f)
		}
	}

	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	client := llm.NewClient(cfg.LMStudioBaseURL, cfg.ModelName)
	client.Start()
	defer client.Stop()
	pdfs := pdfstore.New(cfg.PDFStoragePath)
	growwClient := groww.New(cfg.GrowwTokenPath)
	kiteClient := kite.New(cfg.KiteSessionPath)
	srv, err := web.New(st, client, cfg, pdfs, growwClient, kiteClient)
	if err != nil {
		return fmt.Errorf("init server: %w", err)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("cresto serving on http://localhost%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	return nil
}
