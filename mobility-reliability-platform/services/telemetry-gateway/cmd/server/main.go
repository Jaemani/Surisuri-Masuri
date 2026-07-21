package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Firebase/App Check, Firestore receipt and Cloud Storage adapters are not
	// connected yet. Nil adapters intentionally keep readiness and ingest closed.
	api := httpapi.NewAPI(nil, nil)
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           api.Routes(),
		MaxHeaderBytes:    64 * 1024,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	go func() {
		<-shutdownSignal.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("server_shutdown_failed", "error_code", "shutdown")
		}
	}()

	logger.Info("server_started", "port", port, "ingest_configured", false)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server_failed", "error_code", "listen")
		os.Exit(1)
	}
}
