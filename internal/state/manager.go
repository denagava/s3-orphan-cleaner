package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/denagava/s3-orphan-cleaner/internal/domain"
	"os"
	"path/filepath"
)

var _ domain.StateManager = (*Manager)(nil)

type Manager struct {
	path string
}

func New(path string) *Manager {
	return &Manager{path: path}
}

func (m *Manager) Load() (*domain.State, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // first run — no checkpoint yet
		}
		return nil, fmt.Errorf("read state file %q: %w", m.path, err)
	}

	var s domain.State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state file %q (delete it to start fresh): %w", m.path, err)
	}

	return &s, nil
}
func (m *Manager) Save(s *domain.State) error {
	s.Touch()

	data, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	dir := filepath.Dir(m.path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmpPath, m.path, err)
	}
	return nil

}
