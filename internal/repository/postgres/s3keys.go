package postgres

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

type scanKey struct {
	bun.BaseModel `bun:"table:scan_keys"`
	Key           string `bun:"key,pk"`
	Referenced    bool   `bun:"referenced"`
}

type S3KeysRepository struct {
	db *bun.DB
}

func NewS3KeysRepository(db *bun.DB) *S3KeysRepository {
	return &S3KeysRepository{db: db}
}

func (r *S3KeysRepository) CreateTable(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS scan_keys (
		    key        TEXT    PRIMARY KEY,
		    referenced BOOLEAN NOT NULL DEFAULT false
		)
	`); err != nil {
		return fmt.Errorf("create scan_keys table: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_scan_keys_unreferenced
		    ON scan_keys (key) WHERE referenced = false
	`); err != nil {
		return fmt.Errorf("create index on scan_keys: %w", err)
	}

	return nil
}

const insertChunkSize = 500

func (r *S3KeysRepository) BulkInsert(ctx context.Context, keys []string) error {
	for start := 0; start < len(keys); start += insertChunkSize {
		end := min(start+insertChunkSize, len(keys))
		chunk := keys[start:end]

		rows := make([]scanKey, len(chunk))
		for i, k := range chunk {
			rows[i] = scanKey{Key: k}
		}

		if _, err := r.db.NewInsert().
			Model(&rows).
			On("CONFLICT (key) DO NOTHING").
			Exec(ctx); err != nil {
			return fmt.Errorf("bulk insert [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

const markChunkSize = 500

func (r *S3KeysRepository) MarkReferenced(ctx context.Context, keys []string) error {
	for start := 0; start < len(keys); start += markChunkSize {
		end := min(start+markChunkSize, len(keys))
		chunk := keys[start:end]

		if _, err := r.db.NewUpdate().
			TableExpr("scan_keys").
			Set("referenced = true").
			Where("key IN (?)", bun.In(chunk)).
			Exec(ctx); err != nil {
			return fmt.Errorf("mark referenced [%d:%d]: %w", start, end, err)

		}
	}
	return nil
}

func (r *S3KeysRepository) ListUnreferenced(ctx context.Context, batchSize int, fn func(keys []string) error) error {
	var afterKey string

	for {
		var rows []scanKey

		q := r.db.NewSelect().
			Model(&rows).
			Where("referenced = false").
			OrderExpr("key ASC").
			Limit(batchSize)

		if afterKey != "" {
			q = q.Where("key > ?", afterKey)
		}

		if err := q.Scan(ctx); err != nil {
			return fmt.Errorf("list unreferenced (after=%q): %w", afterKey, err)
		}
		if len(rows) == 0 {
			return nil // all batches processed
		}

		keys := make([]string, len(rows))
		for i, row := range rows {
			keys[i] = row.Key
		}

		if err := fn(keys); err != nil {
			return err
		}

		afterKey = rows[len(rows)-1].Key
	}
}

func (r *S3KeysRepository) DropTable(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, "DROP TABLE IF EXISTS scan_keys"); err != nil {
		return fmt.Errorf("drop scan_keys: %w", err)
	}
	return nil
}
