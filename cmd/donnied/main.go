package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"smartwatch/internal/config"
	"smartwatch/internal/device"
	"smartwatch/internal/server"
	"smartwatch/internal/service"
)

func main() {
	logLevel := slog.LevelInfo
	switch strings.ToLower(os.Getenv("DONNIED_LOG_LEVEL")) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case sig := <-sigCh:
			logger.Info("signal received", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	cfgStore, err := config.New("donnied")
	if err != nil {
		logger.Error("config init failed", "err", err)
		os.Exit(1)
	}

	deviceMgr := device.New(cfgStore, logger)
	svc := service.New(deviceMgr, cancel)
	srv := server.New(svc, logger)

	logger.Info("daemon started", "pid", os.Getpid())

	errCh := make(chan error, 2)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	go func() {
		errCh <- deviceMgr.Run(ctx)
	}()

	firstErr := <-errCh
	if firstErr != nil && !errors.Is(firstErr, context.Canceled) {
		logger.Error("component error", "err", firstErr)
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	select {
	case secondErr := <-errCh:
		if secondErr != nil && !errors.Is(secondErr, context.Canceled) {
			logger.Error("component error", "err", secondErr)
		}
	case <-shutdownCtx.Done():
		logger.Warn("forced shutdown after timeout")
		os.Exit(1)
	}

	logger.Info("daemon stopped gracefully")
}
