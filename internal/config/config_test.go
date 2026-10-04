package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/denagava/s3-orphan-cleaner/internal/config"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

const validYAML = `
database:
  dsn: "postgres://user:pass@localhost/testdb"
s3:
  bucket: "media"
  paths:
    - "uploads/"
    - "images/"
`

func TestLoad_ValidConfig(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Database.DSN != "postgres://user:pass@localhost/testdb" {
		t.Errorf("DSN = %q", cfg.Database.DSN)
	}
	if cfg.S3.Bucket != "media" {
		t.Errorf("Bucket = %q", cfg.S3.Bucket)
	}
	if len(cfg.S3.Paths) != 2 {
		t.Errorf("Paths len = %d, want 2", len(cfg.S3.Paths))
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.S3.Region != "us-east-1" {
		t.Errorf("Region default: got %q, want %q", cfg.S3.Region, "us-east-1")
	}
	if cfg.Cleaner.DBPageSize != 1000 {
		t.Errorf("DBPageSize default: got %d, want 1000", cfg.Cleaner.DBPageSize)
	}
	if cfg.Cleaner.S3DeleteBatchSize != 1000 {
		t.Errorf("S3DeleteBatchSize default: got %d, want 1000", cfg.Cleaner.S3DeleteBatchSize)
	}
	if cfg.Cleaner.StateFile != "state.json" {
		t.Errorf("StateFile default: got %q, want %q", cfg.Cleaner.StateFile, "state.json")
	}
	if cfg.Cleaner.DryRun {
		t.Error("DryRun should default to false")
	}
}

func TestLoad_MissingDSN(t *testing.T) {
	yaml := `
s3:
  bucket: "media"
  paths: ["uploads/"]
`
	_, err := config.Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for missing DSN, got nil")
	}
}

func TestLoad_MissingBucket(t *testing.T) {
	yaml := `
database:
  dsn: "postgres://localhost/db"
s3:
  paths: ["uploads/"]
`
	_, err := config.Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for missing bucket, got nil")
	}
}

func TestLoad_EmptyPaths(t *testing.T) {
	yaml := `
database:
  dsn: "postgres://localhost/db"
s3:
  bucket: "media"
  paths: []
`
	_, err := config.Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for empty paths, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := config.Load(writeConfig(t, "{ not: valid: yaml: ::"))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := config.Load("/no/such/file/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_S3DeleteBatchSizeCappedAt1000(t *testing.T) {
	yaml := `
database:
  dsn: "postgres://localhost/db"
s3:
  bucket: "media"
  paths: ["uploads/"]
cleaner:
  s3_delete_batch_size: 9999
`
	cfg, err := config.Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Cleaner.S3DeleteBatchSize != 1000 {
		t.Errorf("S3DeleteBatchSize should be capped at 1000, got %d", cfg.Cleaner.S3DeleteBatchSize)
	}
}
