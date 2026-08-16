package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kavrynt/kavryctl/internal/manifest"
)

const FileName = "registry.json"

var ErrNotFound = errors.New("server not found")

type Registry struct {
	Version int      `json:"version"`
	Servers []Server `json:"servers"`
}

type Server struct {
	ID           string            `json:"id"`
	Manifest     manifest.Manifest `json:"manifest"`
	Source       string            `json:"source"`
	RegisteredAt time.Time         `json:"registeredAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

func Home(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("KAVRYNT_HOME"); env != "" {
		return env
	}
	return ".kavrynt"
}

func Path(home string) string {
	return filepath.Join(home, FileName)
}

func Init(home string) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create kavrynt home: %w", err)
	}

	path := Path(home)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect registry: %w", err)
	}

	return Save(path, Registry{Version: 1, Servers: []Server{}})
}

func Load(home string) (Registry, error) {
	path := Path(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Registry{}, fmt.Errorf("registry not initialized at %s; run kavryctl init", path)
		}
		return Registry{}, fmt.Errorf("read registry: %w", err)
	}

	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	if r.Version == 0 {
		r.Version = 1
	}
	if r.Servers == nil {
		r.Servers = []Server{}
	}
	for i := range r.Servers {
		if r.Servers[i].ID == "" {
			r.Servers[i].ID = manifest.ServerID(r.Servers[i].Manifest.Metadata)
		}
	}

	return r, nil
}

func Save(path string, r Registry) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

func Register(home string, m manifest.Manifest, source string, now time.Time) (Server, bool, error) {
	if err := Init(home); err != nil {
		return Server{}, false, err
	}

	r, err := Load(home)
	if err != nil {
		return Server{}, false, err
	}

	id := manifest.ServerID(m.Metadata)
	for i := range r.Servers {
		if r.Servers[i].ID == id {
			r.Servers[i].Manifest = m
			r.Servers[i].Source = source
			r.Servers[i].UpdatedAt = now.UTC()
			if err := Save(Path(home), r); err != nil {
				return Server{}, false, err
			}
			return r.Servers[i], false, nil
		}
	}

	server := Server{
		ID:           id,
		Manifest:     m,
		Source:       source,
		RegisteredAt: now.UTC(),
		UpdatedAt:    now.UTC(),
	}
	r.Servers = append(r.Servers, server)
	sortServers(r.Servers)

	if err := Save(Path(home), r); err != nil {
		return Server{}, false, err
	}

	return server, true, nil
}

func List(home string) ([]Server, error) {
	r, err := Load(home)
	if err != nil {
		return nil, err
	}
	sortServers(r.Servers)
	return r.Servers, nil
}

func Inspect(home, id string) (Server, error) {
	r, err := Load(home)
	if err != nil {
		return Server{}, err
	}
	for _, server := range r.Servers {
		if server.ID == id {
			return server, nil
		}
	}
	return Server{}, fmt.Errorf("%w: %q", ErrNotFound, id)
}

func Unregister(home, id string) error {
	r, err := Load(home)
	if err != nil {
		return err
	}
	for i, server := range r.Servers {
		if server.ID == id {
			r.Servers = append(r.Servers[:i], r.Servers[i+1:]...)
			return Save(Path(home), r)
		}
	}
	return fmt.Errorf("%w: %q", ErrNotFound, id)
}

func sortServers(servers []Server) {
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].ID < servers[j].ID
	})
}
