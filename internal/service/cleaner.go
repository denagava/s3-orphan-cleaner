package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/denagava/s3-orphan-cleaner/internal/config"
	"github.com/denagava/s3-orphan-cleaner/internal/domain"
)

type CleanerService struct {
	cfg      *config.Config
	state    domain.StateManager
	dbAssets domain.AssetRepository
	dbKeys   domain.S3KeysRepository
	s3       domain.S3Repository
	logger   *slog.Logger
}

func New(
	cfg *config.Config,
	state domain.StateManager,
	dbAssets domain.AssetRepository,
	dbKeys domain.S3KeysRepository,
	s3 domain.S3Repository,
	logger *slog.Logger,
) *CleanerService {
	return &CleanerService{
		cfg:      cfg,
		state:    state,
		dbAssets: dbAssets,
		dbKeys:   dbKeys,
		s3:       s3,
		logger:   logger,
	}
}

func (s *CleanerService) Run(ctx context.Context) error {
	runStart := time.Now()
	st, err := s.state.Load()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if st == nil {
		st = domain.NewState()
		s.logger.Info("starting fresh run")
	} else {
		s.logger.Info("resuming run",
			"phase", st.Phase,
			"db_rows_scanned", st.DBRowsScanned,
			"delete_count", st.DeletedCount,
		)
	}

	if s.cfg.Cleaner.DryRun {
		s.logger.Warn("DRY RUN MODE — S3 deletions will be skipped")
	}
	if !st.S3ScanDone {
		if err := s.runPhase1(ctx, st); err != nil {
			return fmt.Errorf("phase 1 (s3 scan): %w", err)
		}
	}
	if !st.DBScanDone {
		if err := s.runPhase2(ctx, st); err != nil {
			return fmt.Errorf("phase 2 (db scan): %w", err)
		}
	}
	if !st.DeleteDone {
		if err := s.runPhase3(ctx, st); err != nil {
			return fmt.Errorf("phase 3 (delete): %w", err)
		}
	}
	s.logger.Info("run complete",
		"deleted", st.DeletedCount,
		"db_rows_scanned", st.DBRowsScanned,
		"s3_keys_found", st.S3KeysFound,
		"duration", time.Since(runStart).Round(time.Second),
	)
	return nil
}

func (s *CleanerService) runPhase1(ctx context.Context, st *domain.State) error {
	phaseStart := time.Now()
	s.logger.Info("phase 1: scanning S3", "paths", s.cfg.S3.Paths)
	if len(st.ProcessedPaths) == 0 {
		if err := s.dbKeys.DropTable(ctx); err != nil {
			return fmt.Errorf("drop existing temp table: %w", err)
		}
		if err := s.dbKeys.CreateTable(ctx); err != nil {
			return fmt.Errorf("create temp table: %w", err)
		}
	} else {
		if err := s.dbKeys.CreateTable(ctx); err != nil {
			return fmt.Errorf("create temp table: %w", err)
		}
		s.logger.Info("resuming phase 1", "already_processed", st.ProcessedPaths)
	}

	var totalKeys int64

	for _, prefix := range s.cfg.S3.Paths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cancelled: %w", err)
		}
		if slices.Contains(st.ProcessedPaths, prefix) {
			s.logger.Info("prefix already scanned, skipping", "prefix", prefix)
			continue
		}
		var prefixKeys int64
		if err := s.s3.ListAllKeys(ctx, prefix, func(keys []string) error {
			if err := s.dbKeys.BulkInsert(ctx, keys); err != nil {
				return fmt.Errorf("bulk insert: %w", err)
			}
			prefixKeys += int64(len(keys))
			totalKeys += int64(len(keys))
			return nil
		}); err != nil {
			return fmt.Errorf("list prefix %q: %w", prefix, err)
		}
		s.logger.Info("prefix scanned", "prefix", prefix, "keys", prefixKeys)
		st.ProcessedPaths = append(st.ProcessedPaths, prefix)
		st.S3KeysFound = totalKeys
		if err := s.state.Save(st); err != nil {
			return fmt.Errorf("save state after prefix %q: %w", prefix, err)
		}

	}

	st.S3KeysFound = totalKeys
	st.S3ScanDone = true
	st.Phase = domain.PhaseDBScan

	if err := s.state.Save(st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	s.logger.Info("phase 1 complete",
		"keys_found", totalKeys,
		"duration", time.Since(phaseStart).Round(time.Second),
	)
	return nil
}

func (s *CleanerService) runPhase2(ctx context.Context, st *domain.State) error {
	phaseStart := time.Now()
	s.logger.Info("phase 2: scanning assets table",
		"cursor", st.DBLastID,
		"rows_already_scanned", st.DBRowsScanned,
	)

	pageSize := s.cfg.Cleaner.DBPageSize

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cancelled: %w", err)
		}

		assets, err := s.dbAssets.FetchPage(ctx, st.DBLastID, pageSize)
		if err != nil {
			return fmt.Errorf("fetch page (cursor=%q): %w", st.DBLastID, err)
		}
		if len(assets) == 0 {
			break // all rows processed
		}

		keys := make([]string, 0, len(assets)*3)
		for _, asset := range assets {
			extracted, parseErr := domain.ExtractS3Keys(asset)
			if parseErr != nil {
				s.logger.Warn("failed to parse asset metadata, skipping",
					"id", asset.ID.String(),
					"kind", asset.Kind,
					"err", parseErr,
				)
				st.ParseErrCount++
				continue
			}
			for _, k := range extracted {
				if norm := domain.NormaliseKey(k); norm != "" {
					keys = append(keys, norm)
				}
			}
		}
		keys = dedupeKeys(keys)

		if len(keys) > 0 {
			if err := s.dbKeys.MarkReferenced(ctx, keys); err != nil {
				return fmt.Errorf("mark referenced: %w", err)
			}
		}

		st.DBLastID = assets[len(assets)-1].ID.String()
		st.DBRowsScanned += int64(len(assets))

		if err := s.state.Save(st); err != nil {
			return fmt.Errorf("save state: %w", err)
		}

		s.logger.Info("page processed",
			"rows", len(assets),
			"keys_marked", len(keys),
			"total_scanned", st.DBRowsScanned,
		)
	}
	// SAFETY: abort if fewer assets than required minimum were found.
	// Protects against deleting all S3 objects when DSN points to wrong DB.
	// Set min_db_assets > 0 in config.yaml to enable. Default 0 = disabled.
	if s.cfg.Cleaner.MinDBAssets > 0 && st.DBRowsScanned < s.cfg.Cleaner.MinDBAssets {
		return fmt.Errorf(
			"SAFETY ABORT: found %d assets in database (min required: %d). "+
				"Proceeding to Phase 3 would delete ALL S3 objects. "+
				"Check database.dsn in config.yaml, or set min_db_assets: 0 to disable",
			st.DBRowsScanned, s.cfg.Cleaner.MinDBAssets,
		)
	}
	if st.ParseErrCount > 0 {
		s.logger.Warn("some assets had unparseable JSON metadata",
			"count", st.ParseErrCount,
			"warning", "their S3 objects were NOT marked referenced - review before proceeding",
		)

	}

	st.DBScanDone = true
	st.Phase = domain.PhaseDelete

	if err := s.state.Save(st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	s.logger.Info("phase 2 complete",
		"rows_scanned", st.DBRowsScanned,
		"duration", time.Since(phaseStart).Round(time.Second),
	)
	return nil
}

func (s *CleanerService) runPhase3(ctx context.Context, st *domain.State) error {
	phaseStart := time.Now()
	s.logger.Info("phase 3: deleting unreferenced objects",
		"dry_run", s.cfg.Cleaner.DryRun,
		"already_deleted", st.DeletedCount,
	)

	batchSize := s.cfg.Cleaner.S3DeleteBatchSize
	var batchCount int

	if err := s.dbKeys.ListUnreferenced(ctx, batchSize, func(keys []string) error {
		batchCount++
		if s.cfg.Cleaner.DryRun {
			s.logger.Info("dry run: would delete",
				"batch", batchCount,
				"count", len(keys),
			)
			return nil
		}
		deleted, err := s.deleteWithRetry(ctx, keys, batchCount)
		if err != nil {
			return fmt.Errorf("delete batch %d: %w", batchCount, err)
		}
		st.DeletedCount += int64(deleted)
		if err := s.state.Save(st); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
		s.logger.Info("delete batch", "batch", batchCount, "count", deleted, "total_deleted", st.DeletedCount)
		return nil
	}); err != nil {
		return err
	}

	if batchCount == 0 {
		s.logger.Info("phase 3: no orphan objects found - nothing to delete")
	}

	if !s.cfg.Cleaner.DryRun {
		st.DeleteDone = true
		st.Phase = domain.PhaseComplete
		if err := s.state.Save(st); err != nil {
			return fmt.Errorf("save final state: %w", err)
		}
	}

	s.logger.Info("phase 3 complete",
		"total_deleted", st.DeletedCount,
		"dry_run", s.cfg.Cleaner.DryRun,
		"duration", time.Since(phaseStart).Round(time.Second),
	)
	return nil
}

const maxDeleteRetries = 3

func (s *CleanerService) deleteWithRetry(ctx context.Context, keys []string, batch int) (int, error) {
	var lastErr error
	for attempt := 0; attempt <= maxDeleteRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * 2 * time.Second
			s.logger.Warn("retrying delete after network error", "batch", batch, "attempt", attempt, "wait", wait, "err", lastErr)
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(wait):
			}
		}
		deleted, err := s.s3.DeleteObjects(ctx, keys)
		var partial domain.PartialDeleteError
		if errors.As(err, &partial) {
			return deleted + s.handlePartialFailures(partial), nil
		}
		if err == nil {
			return deleted, nil
		}
		lastErr = err
	}
	return 0, fmt.Errorf("delete failed after %d retries: %w", maxDeleteRetries, lastErr)
}
func (s *CleanerService) handlePartialFailures(partial domain.PartialDeleteError) int {
	stashPath := s.cfg.Cleaner.StashFile
	if stashPath == "" {
		stashPath = "failed_deletions.jsonl"
	}
	var noSuchKey int
	var toStash []domain.DeleteFailure

	for _, f := range partial.Failures() {
		switch f.Code {
		case "NoSuchKey":
			s.logger.Debug("object already deleted (NoSuchKey)", "key", f.Key)
			noSuchKey++
		default:
			s.logger.Warn("permanent deletion failure - written to stash", "key", f.Key, "code", f.Code, "message", f.Message)
			toStash = append(toStash, f)
		}
	}

	if len(toStash) > 0 {
		s.appendStash(toStash, stashPath)
	}
	return noSuchKey
}
func (s *CleanerService) appendStash(failures []domain.DeleteFailure, path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		s.logger.Error("cannot open stash file", "path", path, "err", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, failure := range failures {
		if encErr := enc.Encode(map[string]string{
			"key":     failure.Key,
			"code":    failure.Code,
			"message": failure.Message,
		}); encErr != nil {
			s.logger.Error("cannot write to stash", "err", encErr)
		}
	}
	s.logger.Info("failures written to stash", "path", path, "count", len(failures))
}

func dedupeKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}
