package service_test

import (
	"context"

	"github.com/denagava/s3-orphan-cleaner/internal/domain"
)

type mockStateManager struct {
	initial *domain.State
	saves   []*domain.State
}

func (m *mockStateManager) Load() (*domain.State, error) {
	return m.initial, nil
}

func (m *mockStateManager) Save(s *domain.State) error {
	cp := *s
	m.saves = append(m.saves, &cp)
	return nil
}

// lastSave returns the most recently saved state.
func (m *mockStateManager) lastSave() *domain.State {
	return m.saves[len(m.saves)-1]
}

type mockAssetRepo struct {
	pages [][]domain.Asset
	calls int
}

func (m *mockAssetRepo) FetchPage(_ context.Context, _ string, _ int) ([]domain.Asset, error) {
	if m.calls >= len(m.pages) {
		return nil, nil
	}
	page := m.pages[m.calls]
	m.calls++
	return page, nil
}

// ── mockS3KeysRepo ───────────────────────────────────────────────────────────

type mockS3KeysRepo struct {
	inserted         []string
	referenced       []string
	unreferencedKeys []string
	tableCreated     bool
}

func (m *mockS3KeysRepo) CreateTable(_ context.Context) error {
	m.tableCreated = true
	return nil
}

func (m *mockS3KeysRepo) BulkInsert(_ context.Context, keys []string) error {
	m.inserted = append(m.inserted, keys...)
	return nil
}

func (m *mockS3KeysRepo) MarkReferenced(_ context.Context, keys []string) error {
	m.referenced = append(m.referenced, keys...)
	return nil
}

func (m *mockS3KeysRepo) ListUnreferenced(_ context.Context, batchSize int, fn func([]string) error) error {
	for i := 0; i < len(m.unreferencedKeys); i += batchSize {
		end := min(i+batchSize, len(m.unreferencedKeys))
		if err := fn(m.unreferencedKeys[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockS3KeysRepo) DropTable(_ context.Context) error { return nil }

type mockS3Repo struct {
	keys             map[string][]string
	deleted          []string
	deleteErr        error
	deletePartialErr domain.PartialDeleteError
}

func (m *mockS3Repo) ListAllKeys(_ context.Context, prefix string, fn func([]string) error) error {
	ks, ok := m.keys[prefix]
	if !ok || len(ks) == 0 {
		return nil
	}
	return fn(ks)
}

func (m *mockS3Repo) DeleteObjects(_ context.Context, keys []string) (int, error) {
	if m.deleteErr != nil {
		return 0, m.deleteErr
	}
	if m.deletePartialErr != nil {
		failSet := make(map[string]bool)
		for _, f := range m.deletePartialErr.Failures() {
			failSet[f.Key] = true
		}
		for _, k := range keys {
			if !failSet[k] {
				m.deleted = append(m.deleted, k)
			}
		}
		err := m.deletePartialErr
		m.deletePartialErr = nil // fires only once
		return len(keys) - len(err.Failures()), err
	}
	m.deleted = append(m.deleted, keys...)
	return len(keys), nil
}

type mockPartialErr struct {
	failures []domain.DeleteFailure
}

func (e *mockPartialErr) Error() string                    { return "mock partial delete error" }
func (e *mockPartialErr) Failures() []domain.DeleteFailure { return e.failures }

var _ domain.PartialDeleteError = (*mockPartialErr)(nil)
