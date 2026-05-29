package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"smartwatch/internal/app"
	"smartwatch/internal/config"
	"smartwatch/internal/device"
	"smartwatch/internal/paths"
	"smartwatch/internal/server"
	"smartwatch/internal/service"
	"smartwatch/internal/sleep"
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

	cfgPath, err := paths.ConfigPath("donnied")
	if err != nil {
		logger.Error("config path failed", "err", err)
		os.Exit(1)
	}
	cfgStore, err := config.NewAt(cfgPath)
	if err != nil {
		logger.Error("config init failed", "err", err, "path", cfgPath)
		os.Exit(1)
	}

	sleepDBPath, err := paths.SleepDBPath("donnied")
	if err != nil {
		logger.Error("sleep db path failed", "err", err)
		os.Exit(1)
	}
	sleepStore, err := sleep.NewStore(sleepDBPath)
	if err != nil {
		logger.Error("sleep store init failed", "err", err, "path", sleepDBPath)
		os.Exit(1)
	}
	defer sleepStore.Close()

	deviceMgr := device.New(cfgStore, logger)
	sleepDevice := app.NewSleepDevice(deviceMgr)
	sleepTracker := sleep.NewTracker(sleepDevice, sleepStore, logger)
	svc := service.New(deviceMgr, sleepTracker, cancel)
	srv := server.New(svc, logger)

	logger.Info("daemon started", "pid", os.Getpid(), "data_dir", dirOf(sleepDBPath), "config", cfgPath, "sleep_db", sleepDBPath)

	errCh := make(chan error, 4)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	go func() {
		errCh <- deviceMgr.Run(ctx)
	}()
	go func() {
		errCh <- sleepTracker.Run(ctx)
	}()
	go func() {
		errCh <- app.RunDeviceEvents(ctx, deviceMgr.Events(), sleepTracker, logger)
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

func dirOf(path string) string {
	return filepath.Dir(path)
}
