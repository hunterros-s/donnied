package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"smartwatch/internal/service"
	"smartwatch/protocol"
)

// Types used by the server, matching service types.
type (
	Info         = service.Info
	BatteryInfo  = service.BatteryInfo
	ScanResult   = service.ScanResult
	DeviceStatus = service.DeviceStatus
)

type Service interface {
	Health(context.Context) error
	Info(context.Context) (*Info, error)
	TriggerShutdown(context.Context) error
	DeviceConnected(context.Context) bool
	DeviceBattery(context.Context) (*BatteryInfo, error)
	DeviceSetTime(context.Context, time.Time) error
	DeviceFind(context.Context) error
	DeviceScan(context.Context) ([]ScanResult, error)
	DeviceSetPairedDevice(context.Context, string, string) error
	DeviceUnpair(context.Context) error
	DeviceStatus(context.Context) (*DeviceStatus, error)
	DeviceGetHRLog(ctx context.Context, day protocol.Day) ([]protocol.HRSample, error)
	DeviceGetSteps(ctx context.Context, dayOffset int) ([]protocol.SportDetail, error)
	DeviceGetSleep(ctx context.Context) ([]protocol.SleepSession, error)
	DeviceGetSpO2(ctx context.Context, daysAgo int) ([]protocol.SpO2Day, error)
	DeviceRealtimeRead(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error)
	DeviceRealtimeStart(ctx context.Context, rt protocol.RtType) error
	DeviceRealtimeStop(ctx context.Context, rt protocol.RtType) error
}

type Server struct {
	service Service
	logger  *slog.Logger
	http    *http.Server
	unix    *http.Server
}

func New(svc Service, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	s := &Server{service: svc, logger: logger}

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /info", s.handleInfo)
	mux.HandleFunc("POST /shutdown", s.handleShutdown)

	mux.HandleFunc("GET /device/connected", s.handleDeviceConnected)
	mux.HandleFunc("GET /device/battery", s.handleDeviceBattery)
	mux.HandleFunc("POST /device/time", s.handleDeviceSetTime)
	mux.HandleFunc("POST /device/find", s.handleDeviceFind)

	mux.HandleFunc("GET /device/scan", s.handleDeviceScan)
	mux.HandleFunc("POST /device/pair", s.handleDevicePair)
	mux.HandleFunc("POST /device/unpair", s.handleDeviceUnpair)
	mux.HandleFunc("GET /device/status", s.handleDeviceStatus)

	mux.HandleFunc("GET /device/hr-log", s.handleDeviceHRLog)
	mux.HandleFunc("GET /device/steps", s.handleDeviceSteps)
	mux.HandleFunc("GET /device/sleep", s.handleDeviceSleep)
	mux.HandleFunc("GET /device/spo2", s.handleDeviceSpO2)
	mux.HandleFunc("POST /device/realtime/read", s.handleRealtimeRead)
	mux.HandleFunc("POST /device/realtime/start", s.handleRealtimeStart)
	mux.HandleFunc("POST /device/realtime/stop", s.handleRealtimeStop)

	// Keep the TCP listener loopback-only. ddctl uses the Unix socket; the TCP
	// listener is for local development/debugging and should not expose device
	// controls to the LAN by default.
	s.http = &http.Server{Addr: "127.0.0.1:8080", Handler: mux}
	s.unix = &http.Server{Handler: mux}
	return s
}

func (s *Server) Run(ctx context.Context) error {
	socketPath := "/tmp/smartwatch.sock"
	_ = os.Remove(socketPath)

	unixLn, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("unix listen: %w", err)
	}
	defer os.Remove(socketPath)
	if err := os.Chmod(socketPath, 0666); err != nil {
		s.logger.Warn("failed to chmod socket", "err", err)
	}

	tcpLn, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("tcp listen: %w", err)
	}

	s.logger.Info("http server listening", "addr", s.http.Addr)
	s.logger.Info("unix socket listening", "path", socketPath)

	httpErrCh := make(chan error, 1)
	unixErrCh := make(chan error, 1)
	go func() { httpErrCh <- s.http.Serve(tcpLn) }()
	go func() { unixErrCh <- s.unix.Serve(unixLn) }()

	var firstErr error
	select {
	case <-ctx.Done():
	case err := <-httpErrCh:
		firstErr = err
	case err := <-unixErrCh:
		firstErr = err
	}

	s.logger.Info("servers shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = s.http.Shutdown(shutdownCtx)
	_ = s.unix.Shutdown(shutdownCtx)

	for _, ch := range []chan error{httpErrCh, unixErrCh} {
		select {
		case err := <-ch:
			if err != nil && !errors.Is(err, http.ErrServerClosed) && firstErr == nil {
				firstErr = err
			}
		case <-shutdownCtx.Done():
			return firstErr
		}
	}
	return firstErr
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Health(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n"))
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.service.Info(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if err := s.service.TriggerShutdown(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("shutting down\n"))
}

func (s *Server) handleDeviceConnected(w http.ResponseWriter, r *http.Request) {
	connected := s.service.DeviceConnected(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"connected": connected})
}

func (s *Server) handleDeviceBattery(w http.ResponseWriter, r *http.Request) {
	info, err := s.service.DeviceBattery(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleDeviceSetTime(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeviceSetTime(r.Context(), time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("time synced\n"))
}

func (s *Server) handleDeviceFind(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeviceFind(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("finding device\n"))
}

func (s *Server) handleDeviceScan(w http.ResponseWriter, r *http.Request) {
	results, err := s.service.DeviceScan(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleDevicePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr string `json:"addr"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Addr == "" {
		http.Error(w, "addr required", http.StatusBadRequest)
		return
	}
	if err := s.service.DeviceSetPairedDevice(r.Context(), req.Addr, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("connecting\n"))
}

func (s *Server) handleDeviceUnpair(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeviceUnpair(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("unpaired\n"))
}

func (s *Server) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.service.DeviceStatus(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleDeviceHRLog(w http.ResponseWriter, r *http.Request) {
	day, err := parseDay(r.URL.Query().Get("day"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	samples, err := s.service.DeviceGetHRLog(r.Context(), day)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(samples)
}

func (s *Server) handleDeviceSteps(w http.ResponseWriter, r *http.Request) {
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		var err error
		offset, err = strconv.Atoi(v)
		if err != nil {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}
	}
	details, err := s.service.DeviceGetSteps(r.Context(), offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func (s *Server) handleDeviceSleep(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.service.DeviceGetSleep(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleDeviceSpO2(w http.ResponseWriter, r *http.Request) {
	daysAgo := 0
	if v := r.URL.Query().Get("days_ago"); v != "" {
		var err error
		daysAgo, err = strconv.Atoi(v)
		if err != nil {
			http.Error(w, "invalid days_ago", http.StatusBadRequest)
			return
		}
	}
	days, err := s.service.DeviceGetSpO2(r.Context(), daysAgo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(days)
}

func (s *Server) handleRealtimeRead(w http.ResponseWriter, r *http.Request) {
	rt, ok := decodeRealtimeType(w, r)
	if !ok {
		return
	}
	reading, err := s.service.DeviceRealtimeRead(r.Context(), rt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reading)
}

func (s *Server) handleRealtimeStart(w http.ResponseWriter, r *http.Request) {
	rt, ok := decodeRealtimeType(w, r)
	if !ok {
		return
	}
	if err := s.service.DeviceRealtimeStart(r.Context(), rt); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("started\n"))
}

func (s *Server) handleRealtimeStop(w http.ResponseWriter, r *http.Request) {
	rt, ok := decodeRealtimeType(w, r)
	if !ok {
		return
	}
	if err := s.service.DeviceRealtimeStop(r.Context(), rt); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("stopped\n"))
}

func decodeRealtimeType(w http.ResponseWriter, r *http.Request) (protocol.RtType, bool) {
	var req struct {
		Type int `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return 0, false
	}
	if req.Type == 0 {
		req.Type = int(protocol.RtHeartRate)
	}
	return protocol.RtType(req.Type), true
}

func parseDay(s string) (protocol.Day, error) {
	if s == "" || s == "today" {
		return protocol.Today(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return protocol.Day{}, fmt.Errorf("invalid day format (use YYYY-MM-DD or \"today\"): %w", err)
	}
	return protocol.Day{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}
