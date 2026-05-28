package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Config is the persisted daemon configuration.
type Config struct {
	PairedAddr string `json:"paired_addr"`
	PairedName string `json:"paired_name"`
}

// Store persists Config to disk and caches it in memory.
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

// New loads (or creates) a Store for the given application name.
func New(appName string) (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, appName, "config.json")

	s := &Store{path: path}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return s, nil
}

// Load returns the current in-memory Config.
func (s *Store) Load() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Save persists cfg to disk and updates the in-memory copy.
func (s *Store) Save(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return err
	}

	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}
