package domain

import (
	"context"
)

type AssetRepository interface {
	FetchPage(ctx context.Context, afterID string, limit int) ([]Asset, error)
}
type S3KeysRepository interface {
	CreateTable(ctx context.Context) error
	BulkInsert(ctx context.Context, keys []string) error
	MarkReferenced(ctx context.Context, keys []string) error
	ListUnreferenced(ctx context.Context, batchSize int, fn func(keys []string) error) error
	DropTable(ctx context.Context) error
}
type S3Repository interface {
	ListAllKeys(ctx context.Context, prefix string, fn func(keys []string) error) error
	DeleteObjects(ctx context.Context, keys []string) (deleted int, err error)
}
type StateManager interface {
	Load() (*State, error)
	Save(s *State) error
}
