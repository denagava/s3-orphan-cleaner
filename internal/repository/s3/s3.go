package s3repo

import (
	"context"
	"fmt"
	"time"

	"github.com/denagava/s3-orphan-cleaner/internal/config"
	"github.com/denagava/s3-orphan-cleaner/internal/domain"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	_ domain.S3Repository       = (*Repository)(nil)
	_ domain.PartialDeleteError = (*DeletePartialError)(nil)
)

type Repository struct {
	client *awss3.Client
	cfg    *config.S3Config
}

func New(client *awss3.Client, cfg *config.S3Config) *Repository {
	return &Repository{client: client, cfg: cfg}
}
func (r *Repository) ListAllKeys(ctx context.Context, prefix string, fn func(keys []string) error) error {
	var cutoff time.Time
	if r.cfg.OlderThan != "" {
		if age, err := time.ParseDuration(r.cfg.OlderThan); err == nil {
			cutoff = time.Now().Add(-age)
		}
	}
	paginator := awss3.NewListObjectsV2Paginator(r.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(r.cfg.Bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list objects v2 (bucket=%q prefix=%q): %w", r.cfg.Bucket, prefix, err)
		}
		if len(page.Contents) == 0 {
			continue
		}
		keys := make([]string, 0, len(page.Contents))
		for _, obj := range page.Contents {
			if obj.Key == nil || *obj.Key == "" {
				continue
			}
			if !cutoff.IsZero() && obj.LastModified != nil && obj.LastModified.After(cutoff) {
				continue
			}
			keys = append(keys, *obj.Key)
		}
		if len(keys) == 0 {
			continue
		}
		if err := fn(keys); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) DeleteObjects(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	if len(keys) > 1000 {
		return 0, fmt.Errorf("DeleteObjects: max 1000 keys per call, got %d", len(keys))
	}
	objects := make([]types.ObjectIdentifier, len(keys))
	for i, k := range keys {
		objects[i] = types.ObjectIdentifier{Key: aws.String(k)}
	}
	out, err := r.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
		Bucket: aws.String(r.cfg.Bucket),
		Delete: &types.Delete{
			Objects: objects,
			Quiet:   aws.Bool(true),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("delete objects (bucket=%q count=%d): %w", r.cfg.Bucket, len(keys), err)
	}
	if len(out.Errors) > 0 {
		return len(keys) - len(out.Errors), newDeletePartialError(out.Errors)
	}
	return len(keys), nil
}

type DeletePartialError struct {
	failures []domain.DeleteFailure
}

func (e *DeletePartialError) Error() string {
	return fmt.Sprintf("partial delete: %d object/s failed", len(e.failures))
}
func (e *DeletePartialError) Failures() []domain.DeleteFailure {
	return e.failures
}
func newDeletePartialError(errs []types.Error) *DeletePartialError {
	failures := make([]domain.DeleteFailure, len(errs))
	for i, e := range errs {
		failures[i] = domain.DeleteFailure{
			Key:     aws.ToString(e.Key),
			Code:    aws.ToString(e.Code),
			Message: aws.ToString(e.Message),
		}
	}
	return &DeletePartialError{failures: failures}
}
