package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/kavrynt/registry/internal/model"
	"github.com/kavrynt/registry/internal/validation"
)

var ErrNotFound = errors.New("server not found")

type Store interface {
	Upsert(m model.Manifest, now time.Time) (model.ServerRecord, bool, error)
	List() ([]model.ServerRecord, error)
	Get(name string) (model.ServerRecord, error)
	Delete(name string) error
	Count() (int, error)
}

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("store path is required")
	}
	store := &FileStore{path: path}
	if err := store.init(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) Upsert(m model.Manifest, now time.Time) (model.ServerRecord, bool, error) {
	if err := validation.Manifest(m); err != nil {
		return model.ServerRecord{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	registry, err := s.loadLocked()
	if err != nil {
		return model.ServerRecord{}, false, err
	}

	name := m.Metadata.Name
	for i := range registry.Servers {
		if registry.Servers[i].Manifest.Metadata.Name == name {
			registry.Servers[i].Manifest = m
			registry.Servers[i].UpdatedAt = now.UTC()
			if err := s.saveLocked(registry); err != nil {
				return model.ServerRecord{}, false, err
			}
			return registry.Servers[i], false, nil
		}
	}

	record := model.ServerRecord{
		Manifest:     m,
		RegisteredAt: now.UTC(),
		UpdatedAt:    now.UTC(),
	}
	registry.Servers = append(registry.Servers, record)
	sortRecords(registry.Servers)
	if err := s.saveLocked(registry); err != nil {
		return model.ServerRecord{}, false, err
	}
	return record, true, nil
}

func (s *FileStore) List() ([]model.ServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	registry, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	sortRecords(registry.Servers)
	return append([]model.ServerRecord(nil), registry.Servers...), nil
}

func (s *FileStore) Get(name string) (model.ServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	registry, err := s.loadLocked()
	if err != nil {
		return model.ServerRecord{}, err
	}
	for _, server := range registry.Servers {
		if server.Manifest.Metadata.Name == name {
			return server, nil
		}
	}
	return model.ServerRecord{}, ErrNotFound
}

func (s *FileStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	registry, err := s.loadLocked()
	if err != nil {
		return err
	}
	for i, server := range registry.Servers {
		if server.Manifest.Metadata.Name == name {
			registry.Servers = append(registry.Servers[:i], registry.Servers[i+1:]...)
			return s.saveLocked(registry)
		}
	}
	return ErrNotFound
}

func (s *FileStore) Count() (int, error) {
	servers, err := s.List()
	if err != nil {
		return 0, err
	}
	return len(servers), nil
}

func (s *FileStore) init() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create store directory: %w", err)
	}
	if _, err := os.Stat(s.path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect store: %w", err)
	}
	return s.saveLocked(model.Registry{Version: 1, Servers: []model.ServerRecord{}})
}

func (s *FileStore) loadLocked() (model.Registry, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return model.Registry{}, fmt.Errorf("read registry: %w", err)
	}
	var registry model.Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return model.Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	if registry.Version == 0 {
		registry.Version = 1
	}
	if registry.Servers == nil {
		registry.Servers = []model.ServerRecord{}
	}
	return registry, nil
}

func (s *FileStore) saveLocked(registry model.Registry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".registry-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp registry: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp registry: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod temp registry: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace registry: %w", err)
	}
	return nil
}

func sortRecords(records []model.ServerRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Manifest.Metadata.Name < records[j].Manifest.Metadata.Name
	})
}
