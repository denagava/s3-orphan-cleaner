package service_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/denagava/s3-orphan-cleaner/internal/config"
	"github.com/denagava/s3-orphan-cleaner/internal/domain"
	"github.com/denagava/s3-orphan-cleaner/internal/service"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() *config.Config {
	return &config.Config{
		Database: config.DatabaseConfig{DSN: "test-dsn"},
		S3: config.S3Config{
			Bucket: "test-bucket",
			Paths:  []string{"uploads/"},
		},
		Cleaner: config.CleanerConfig{
			DBPageSize:        2, // small page size to exercise pagination
			S3DeleteBatchSize: 10,
			StateFile:         "test-state.json",
			DryRun:            false,
		},
	}
}

func imageAsset(sourceKey string) domain.Asset {
	props, _ := json.Marshal(map[string]any{
		"image": map[string]any{
			"renditions": map[string]any{"original": sourceKey},
		},
	})
	return domain.Asset{
		ID:       uuid.New(),
		Kind:     "IMAGE",
		Metadata: json.RawMessage(props),
	}
}

func mustContain(t *testing.T, haystack []string, needle string) {
	t.Helper()
	if !slices.Contains(haystack, needle) {
		t.Errorf("expected %q in %v", needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack []string, needle string) {
	t.Helper()
	if slices.Contains(haystack, needle) {
		t.Errorf("did not expect %q in %v", needle, haystack)
	}
}

func TestRun_FullRun(t *testing.T) {
	s3 := &mockS3Repo{
		keys: map[string][]string{
			"uploads/": {"uploads/a.jpg", "uploads/b.jpg", "uploads/c.jpg"},
		},
	}
	keysRepo := &mockS3KeysRepo{
		unreferencedKeys: []string{"uploads/b.jpg", "uploads/c.jpg"},
	}
	assetRepo := &mockAssetRepo{
		pages: [][]domain.Asset{
			{imageAsset("uploads/a.jpg")},
		},
	}
	stateMgr := &mockStateManager{}

	svc := service.New(testConfig(), stateMgr, assetRepo, keysRepo, s3, discardLogger())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	mustContain(t, keysRepo.inserted, "uploads/a.jpg")
	mustContain(t, keysRepo.inserted, "uploads/b.jpg")
	mustContain(t, keysRepo.inserted, "uploads/c.jpg")

	mustContain(t, keysRepo.referenced, "uploads/a.jpg")

	mustContain(t, s3.deleted, "uploads/b.jpg")
	mustContain(t, s3.deleted, "uploads/c.jpg")
	mustNotContain(t, s3.deleted, "uploads/a.jpg")

	final := stateMgr.lastSave()
	if !final.S3ScanDone {
		t.Error("S3ScanDone should be true")
	}
	if !final.DBScanDone {
		t.Error("DBScanDone should be true")
	}
	if !final.DeleteDone {
		t.Error("DeleteDone should be true")
	}
	if final.DeletedCount != 2 {
		t.Errorf("DeletedCount = %d, want 2", final.DeletedCount)
	}
}

func TestRun_SkipsCompletedPhases(t *testing.T) {
	s3 := &mockS3Repo{keys: map[string][]string{"uploads/": {"uploads/x.jpg"}}}
	keysRepo := &mockS3KeysRepo{unreferencedKeys: []string{"uploads/x.jpg"}}
	assetRepo := &mockAssetRepo{}
	resumeState := domain.NewState()
	resumeState.S3ScanDone = true
	resumeState.Phase = domain.PhaseDBScan

	stateMgr := &mockStateManager{initial: resumeState}

	svc := service.New(testConfig(), stateMgr, assetRepo, keysRepo, s3, discardLogger())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if keysRepo.tableCreated {
		t.Error("CreateTable should not be called when S3ScanDone=true")
	}
	if len(keysRepo.inserted) > 0 {
		t.Errorf("BulkInsert should not be called, got %v", keysRepo.inserted)
	}

	mustContain(t, s3.deleted, "uploads/x.jpg")
}

func TestRun_DryRun(t *testing.T) {
	s3 := &mockS3Repo{keys: map[string][]string{"uploads/": {"uploads/dry.jpg"}}}
	keysRepo := &mockS3KeysRepo{unreferencedKeys: []string{"uploads/dry.jpg"}}
	assetRepo := &mockAssetRepo{}
	stateMgr := &mockStateManager{}

	cfg := testConfig()
	cfg.Cleaner.DryRun = true

	svc := service.New(cfg, stateMgr, assetRepo, keysRepo, s3, discardLogger())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(s3.deleted) > 0 {
		t.Errorf("DeleteObjects must not be called in dry_run, deleted: %v", s3.deleted)
	}

	final := stateMgr.lastSave()
	if final.DeleteDone {
		t.Error("DeleteDone should be false after a dry run")
	}
}

func TestRun_PartialDeleteError(t *testing.T) {
	s3 := &mockS3Repo{
		keys: map[string][]string{"uploads/": {}},
		deletePartialErr: &mockPartialErr{
			failures: []domain.DeleteFailure{
				{Key: "uploads/denied.jpg", Code: "AccessDenied", Message: "access denied"},
			},
		},
	}
	keysRepo := &mockS3KeysRepo{
		unreferencedKeys: []string{"uploads/ok1.jpg", "uploads/denied.jpg", "uploads/ok2.jpg"},
	}
	assetRepo := &mockAssetRepo{}
	stateMgr := &mockStateManager{
		initial: &domain.State{S3ScanDone: true, DBScanDone: true},
	}

	svc := service.New(testConfig(), stateMgr, assetRepo, keysRepo, s3, discardLogger())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run() should not return error on partial failure, got: %v", err)
	}

	final := stateMgr.lastSave()
	if !final.DeleteDone {
		t.Error("DeleteDone should be true even after partial failures")
	}
	if final.DeletedCount != 2 {
		t.Errorf("DeletedCount = %d, want 2", final.DeletedCount)
	}
}

func TestRun_SavesStateAfterEachPage(t *testing.T) {
	asset1 := imageAsset("uploads/x1.jpg")
	asset2 := imageAsset("uploads/x2.jpg")
	asset3 := imageAsset("uploads/x3.jpg")

	assetRepo := &mockAssetRepo{
		pages: [][]domain.Asset{
			{asset1, asset2},
			{asset3},
		},
	}
	keysRepo := &mockS3KeysRepo{}
	s3 := &mockS3Repo{keys: map[string][]string{"uploads/": {}}}
	stateMgr := &mockStateManager{}

	svc := service.New(testConfig(), stateMgr, assetRepo, keysRepo, s3, discardLogger())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	var phase2Saves []*domain.State
	for _, s := range stateMgr.saves {
		if s.DBRowsScanned > 0 {
			phase2Saves = append(phase2Saves, s)
		}
	}

	if len(phase2Saves) < 2 {
		t.Fatalf("expected at least 2 Phase 2 saves, got %d", len(phase2Saves))
	}

	if phase2Saves[0].DBRowsScanned != 2 {
		t.Errorf("after page 1: DBRowsScanned = %d, want 2", phase2Saves[0].DBRowsScanned)
	}
	if phase2Saves[0].DBLastID != asset2.ID.String() {
		t.Errorf("after page 1: DBLastID = %q, want %q", phase2Saves[0].DBLastID, asset2.ID.String())
	}

	if phase2Saves[1].DBRowsScanned != 3 {
		t.Errorf("after page 2: DBRowsScanned = %d, want 3", phase2Saves[1].DBRowsScanned)
	}
	if phase2Saves[1].DBLastID != asset3.ID.String() {
		t.Errorf("after page 2: DBLastID = %q, want %q", phase2Saves[1].DBLastID, asset3.ID.String())
	}
}

func TestRun_MultipleS3Paths(t *testing.T) {
	cfg := testConfig()
	cfg.S3.Paths = []string{"uploads/", "images/", "documents/"}

	s3 := &mockS3Repo{
		keys: map[string][]string{
			"uploads/":   {"uploads/file.jpg"},
			"images/":    {"images/photo.png"},
			"documents/": {"documents/report.pdf"},
		},
	}
	keysRepo := &mockS3KeysRepo{}
	assetRepo := &mockAssetRepo{}
	stateMgr := &mockStateManager{}

	svc := service.New(cfg, stateMgr, assetRepo, keysRepo, s3, discardLogger())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// All keys from all three prefixes must have been inserted.
	mustContain(t, keysRepo.inserted, "uploads/file.jpg")
	mustContain(t, keysRepo.inserted, "images/photo.png")
	mustContain(t, keysRepo.inserted, "documents/report.pdf")
}
