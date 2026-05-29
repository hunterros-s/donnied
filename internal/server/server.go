package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"smartwatch/internal/api"
	"smartwatch/internal/web"
	"smartwatch/protocol"
)

type Service interface {
	Health(context.Context) error
	State(context.Context, api.StateOptions) (*api.AppState, error)
	TriggerShutdown(context.Context) error
	DeviceScan(context.Context) ([]api.ScanResult, error)
	DeviceSetPairedDevice(context.Context, string, string) error
	DeviceUnpair(context.Context) error
	DeviceSetTime(context.Context, time.Time) error
	DeviceFind(context.Context) error
	DeviceRealtimeRead(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error)
	DeviceRealtimeStart(ctx context.Context, rt protocol.RtType) error
	DeviceRealtimeStop(ctx context.Context, rt protocol.RtType) error
	Sync(ctx context.Context, kinds []string) error
}

type Server struct {
	service Service
	logger  *slog.Logger
	http    *http.Server
	unix    *http.Server
}

func New(svc Service, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	httpAddr := envString("DONNIED_HTTP_ADDR", "0.0.0.0:80")
	s := &Server{
		service: svc,
		logger:  logger,
	}

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/actions/shutdown", s.handleShutdown)
	mux.HandleFunc("POST /api/actions/scan", s.handleScan)
	mux.HandleFunc("POST /api/actions/pair", s.handlePair)
	mux.HandleFunc("POST /api/actions/unpair", s.handleUnpair)
	mux.HandleFunc("POST /api/actions/sync-time", s.handleSyncTime)
	mux.HandleFunc("POST /api/actions/find-device", s.handleFindDevice)
	mux.HandleFunc("POST /api/actions/sync", s.handleSync)
	mux.HandleFunc("POST /api/actions/realtime-read", s.handleRealtimeRead)
	mux.HandleFunc("POST /api/actions/realtime-start", s.handleRealtimeStart)
	mux.HandleFunc("POST /api/actions/realtime-stop", s.handleRealtimeStop)
	mux.HandleFunc("GET /api", http.NotFound)
	mux.HandleFunc("GET /api/", http.NotFound)
	mux.Handle("GET /", web.Handler())

	// ddctl uses the Unix socket. The TCP listener serves the browser UI.
	// By default it is available on the LAN by IP address.
	s.http = &http.Server{Addr: httpAddr, Handler: mux}
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

	s.logger.Info("http server listening", "addr", tcpLn.Addr().String(), "configured_addr", s.http.Addr, "urls", httpURLs(tcpLn))
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

func envString(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func httpURLs(tcpLn net.Listener) []string {
	tcpAddr, ok := tcpLn.Addr().(*net.TCPAddr)
	if !ok || tcpAddr.Port <= 0 {
		return nil
	}
	urls := []string{httpURL("127.0.0.1", tcpAddr.Port)}
	for _, ip := range lanIPs() {
		urls = append(urls, httpURL(ip.String(), tcpAddr.Port))
	}
	return urls
}

func httpURL(host string, port int) string {
	if port == 80 {
		return fmt.Sprintf("http://%s/", host)
	}
	return fmt.Sprintf("http://%s:%d/", host, port)
}

func lanIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				ips = append(ips, ip4)
			}
		}
	}
	return ips
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Health(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n"))
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	opts, err := parseStateOptions(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, err := s.service.State(r.Context(), opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if err := s.service.TriggerShutdown(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, api.ActionResult{Message: "shutting down"})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	results, err := s.service.DeviceScan(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	state, err := s.service.State(r.Context(), api.StateOptions{IncludeHistory: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, api.ActionResult{ScanResults: results, State: state})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var req api.PairRequest
	if !decodeJSON(w, r, &req) {
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
	s.writeActionState(w, r, http.StatusAccepted, "connecting")
}

func (s *Server) handleUnpair(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeviceUnpair(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeActionState(w, r, http.StatusAccepted, "unpaired")
}

func (s *Server) handleSyncTime(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeviceSetTime(r.Context(), time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeActionState(w, r, http.StatusAccepted, "time synced")
}

func (s *Server) handleFindDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeviceFind(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeActionState(w, r, http.StatusAccepted, "finding device")
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var req api.SyncRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.service.Sync(r.Context(), req.Kinds); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	s.writeActionState(w, r, http.StatusAccepted, "synced")
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
	state, err := s.service.State(r.Context(), api.StateOptions{IncludeHistory: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, api.ActionResult{RealtimeReading: reading, State: state})
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
	s.writeActionState(w, r, http.StatusAccepted, "started")
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
	s.writeActionState(w, r, http.StatusAccepted, "stopped")
}

func (s *Server) writeActionState(w http.ResponseWriter, r *http.Request, code int, message string) {
	state, err := s.service.State(r.Context(), api.StateOptions{IncludeHistory: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, code, api.ActionResult{Message: message, State: state})
}

func decodeRealtimeType(w http.ResponseWriter, r *http.Request) (protocol.RtType, bool) {
	var req api.RealtimeRequest
	if !decodeJSON(w, r, &req) {
		return 0, false
	}
	if req.Type == 0 {
		req.Type = int(protocol.RtHeartRate)
	}
	return protocol.RtType(req.Type), true
}

func parseStateOptions(r *http.Request) (api.StateOptions, error) {
	q := r.URL.Query()
	day, err := parseDay(q.Get("day"))
	if err != nil {
		return api.StateOptions{}, err
	}
	from, err := parseOptionalTime(q.Get("from"))
	if err != nil {
		return api.StateOptions{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := parseOptionalTime(q.Get("to"))
	if err != nil {
		return api.StateOptions{}, fmt.Errorf("invalid to: %w", err)
	}
	includeWatch, watchKinds := parseWatchHistory(q.Get("watch_history"))
	includeHistory := true
	if v := q.Get("history"); v != "" {
		includeHistory = boolQuery(v)
	}
	return api.StateOptions{
		Day:                 day,
		From:                from,
		To:                  to,
		IncludeHistory:      includeHistory || includeWatch,
		IncludeWatchHistory: includeWatch,
		WatchHistoryKinds:   watchKinds,
	}, nil
}

func parseDay(s string) (time.Time, error) {
	if s == "" || s == "today" {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local), nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day format (use YYYY-MM-DD or today): %w", err)
	}
	return t, nil
}

func parseOptionalTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("use RFC3339 timestamp or YYYY-MM-DD")
}

func boolQuery(s string) bool {
	if s == "" {
		return false
	}
	v, err := strconv.ParseBool(s)
	return err == nil && v
}

func parseWatchHistory(s string) (bool, map[string]bool) {
	if s == "" {
		return false, nil
	}
	if boolQuery(s) {
		return true, nil // true means all watch-backed history.
	}
	kinds := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		kind := strings.TrimSpace(part)
		if kind != "" {
			kinds[kind] = true
		}
	}
	return len(kinds) > 0, kinds
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}
