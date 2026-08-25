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

	"github.com/ligson/water/water-be/internal/api"
	"github.com/ligson/water/water-be/internal/auth"
	"github.com/ligson/water/water-be/internal/config"
	"github.com/ligson/water/water-be/internal/store"
)

var buildVersion = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	logger.Info("若水 starting", "version", buildVersion)

	cfg := config.Load()

	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}

	bootstrapPIN, err := auth.NewStore(db).Ensure(context.Background(), cfg.AccessPIN)
	if err != nil {
		logger.Error("initialize auth", "error", err)
		os.Exit(1)
	}
	cfg.AuthEnabled = true
	if bootstrapPIN != "" {
		logger.Warn("若水 access PIN generated", "pin", bootstrapPIN)
	} else if cfg.AccessPIN != "" {
		logger.Info("若水 access PIN loaded from WATER_ACCESS_PIN")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(db, cfg, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("若水 backend listening", "addr", cfg.HTTPAddr, "db", cfg.DatabasePath)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown server", "error", err)
		os.Exit(1)
	}

	logger.Info("若水 backend stopped")
}
