package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/denagava/s3-orphan-cleaner/internal/domain"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var mediaTypeValues = []string{
	string(domain.AssetKindImage),
	string(domain.AssetKindDocument),
	string(domain.AssetKindPDF),
	string(domain.AssetKindPresentation),
	string(domain.AssetKindSpreadsheet),
}

type AssetRow struct {
	bun.BaseModel `bun:"table:public.assets,alias:e"`

	ID       uuid.UUID       `bun:"id,pk,type:uuid"`
	Kind     string          `bun:"kind"`
	Metadata json.RawMessage `bun:"metadata,type:jsonb"`
}

func toDomain(row AssetRow) domain.Asset {
	return domain.Asset{
		ID:       row.ID,
		Kind:     row.Kind,
		Metadata: row.Metadata,
	}

}

type AssetRepository struct {
	db *bun.DB
}

func NewAssetRepository(db *bun.DB) *AssetRepository {
	return &AssetRepository{db: db}
}

func (r *AssetRepository) FetchPage(ctx context.Context, afterID string, limit int) ([]domain.Asset, error) {
	var rows []AssetRow
	q := r.db.NewSelect().
		Model(&rows).
		Where("kind IN (?)", bun.In(mediaTypeValues)).
		OrderExpr("id ASC").
		Limit(limit)

	if afterID != "" {
		id, err := uuid.Parse(afterID)
		if err != nil {
			return nil, fmt.Errorf("parse cursor id %q: %w", afterID, err)
		}
		q = q.Where("id > ?", id)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("fetch page (after=%q): %w", afterID, err)
	}
	result := make([]domain.Asset, len(rows))
	for i, row := range rows {
		result[i] = toDomain(row)
	}
	return result, nil
}
