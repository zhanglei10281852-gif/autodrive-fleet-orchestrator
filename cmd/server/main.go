package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/config"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/httpapi"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/middleware"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := newLogger(cfg.LogLevel)
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := sqlite.Open(rootCtx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open persistence: %w", err)
	}
	defer store.Close()
	businessClock := clock.System{}
	ids := idgen.Crypto{}
	authService := service.NewAuth(store, businessClock, ids, cfg.SessionTTL)
	if err := authService.Bootstrap(rootCtx, cfg.BootstrapAdmin, cfg.BootstrapPassword); err != nil {
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	fleetService := service.NewFleet(store, businessClock, ids)
	dispatchService := service.NewDispatch(store, businessClock, ids)
	operationsService := service.NewOperations(store, businessClock, ids, 5*time.Minute)
	resourceService := service.NewResource(store, businessClock, ids)
	api := httpapi.New(authService, fleetService, dispatchService, operationsService, resourceService,
		store, logger, cfg.MaxRequestBytes)
	handler := middleware.Chain(api.Handler(),
		middleware.RequestID(ids),
		middleware.AccessLog(logger),
		middleware.Recover(logger, httpapi.WriteError),
	)
	server := &http.Server{
		Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	outbox := worker.NewOutbox(store, worker.LogPublisher{Logger: logger}, businessClock, logger,
		"server-primary", cfg.WorkerInterval, cfg.WorkerLease)
	housekeeping := worker.NewHousekeeping(store, businessClock, logger, time.Minute, 30*24*time.Hour)
	workerErrors := make(chan error, 2)
	go func() { workerErrors <- outbox.Run(rootCtx) }()
	go func() { workerErrors <- housekeeping.Run(rootCtx) }()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("autodrive orchestration server listening", "address", cfg.Address, "database", cfg.DatabasePath)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-rootCtx.Done():
	case err := <-workerErrors:
		if err != nil && !errors.Is(err, context.Canceled) {
			stop()
			return fmt.Errorf("background worker stopped: %w", err)
		}
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
			return fmt.Errorf("http server stopped: %w", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("autodrive orchestration server stopped")
	return nil
}

func newLogger(level string) *slog.Logger {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		parsed = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed})
	return slog.New(handler).With("service", "autodrive-fleet-orchestrator")
}
