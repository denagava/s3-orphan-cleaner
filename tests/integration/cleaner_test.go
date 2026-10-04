//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/uptrace/bun"

	"github.com/denagava/s3-orphan-cleaner/internal/config"
	"github.com/denagava/s3-orphan-cleaner/internal/repository/postgres"
	s3repo "github.com/denagava/s3-orphan-cleaner/internal/repository/s3"
	"github.com/denagava/s3-orphan-cleaner/internal/service"
	"github.com/denagava/s3-orphan-cleaner/internal/state"
)

const (
	testBucket    = "s3-orphan-cleaner-test"
	minioUser     = "minioadmin"
	minioPassword = "minioadmin"
)

var (
	globalPGDSN    string
	globalMinioURL string
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	os.Exit(mustSetup(ctx, m))
}

func mustSetup(ctx context.Context, m *testing.M) int {
	pgC, dsn, err := startPostgres(ctx)
	if err != nil {
		log.Fatalf("start postgres: %v", err)
	}
	defer pgC.Terminate(ctx)
	globalPGDSN = dsn
	log.Printf("postgres ready: %s", dsn)

	minioC, url, err := startMinio(ctx)
	if err != nil {
		log.Fatalf("start minio: %v", err)
	}
	defer minioC.Terminate(ctx)
	globalMinioURL = url
	log.Printf("minio ready: %s", url)

	db := openDB()
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS public.assets (
		    id         UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
		    kind       TEXT  NOT NULL,
		    metadata   JSONB NOT NULL DEFAULT '{}'
		)
	`); err != nil {
		log.Fatalf("create assets table: %v", err)
	}

	s3c := buildS3Client(ctx)
	s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(testBucket)})
	return m.Run()
}

func startPostgres(ctx context.Context) (testcontainers.Container, string, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "postgres:16-alpine",
			Env: map[string]string{
				"POSTGRES_DB":       "testdb",
				"POSTGRES_USER":     "test",
				"POSTGRES_PASSWORD": "test",
			},
			ExposedPorts: []string{"5432/tcp"},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, "", err
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	return c, fmt.Sprintf("postgres://test:test@%s:%s/testdb?sslmode=disable", host, port.Port()), nil
}

func startMinio(ctx context.Context) (testcontainers.Container, string, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/minio/minio:RELEASE.2025-06-13T11-33-47Z",
			Env:          map[string]string{"MINIO_ROOT_USER": minioUser, "MINIO_ROOT_PASSWORD": minioPassword},
			Cmd:          []string{"server", "/data"},
			ExposedPorts: []string{"9000/tcp"},
			WaitingFor:   wait.ForListeningPort("9000/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, "", err
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "9000/tcp")
	return c, fmt.Sprintf("http://%s:%s", host, port.Port()), nil
}

func openDB() *bun.DB {
	return postgres.OpenDB(globalPGDSN)
}

func buildS3Client(ctx context.Context) *awss3.Client {
	client, err := s3repo.NewClient(ctx, &config.S3Config{
		Endpoint: globalMinioURL, Region: "us-east-1", Bucket: testBucket,
		AccessKeyID: minioUser, SecretAccessKey: minioPassword, UsePathStyle: true,
	})
	if err != nil {
		log.Fatalf("s3 client: %v", err)
	}
	return client
}

func resetTestData(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	db := openDB()
	defer db.Close()
	db.ExecContext(ctx, "TRUNCATE public.assets")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS scan_keys")
}

func insertImageAsset(t *testing.T, db *bun.DB, s3Key string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO public.assets (id, kind, metadata)
		 VALUES (gen_random_uuid(), 'IMAGE', ?::jsonb)`,
		fmt.Sprintf(`{"image":{"renditions":{"original":%q}}}`, s3Key),
	)
	if err != nil {
		t.Fatalf("insertImageAsset(%q): %v", s3Key, err)
	}
}

func insertDocumentAsset(t *testing.T, db *bun.DB, s3Key string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO public.assets (id, kind, metadata)
		 VALUES (gen_random_uuid(), 'DOCUMENT', ?::jsonb)`,
		fmt.Sprintf(`{"document":{"sourceKey":%q}}`, s3Key),
	)
	if err != nil {
		t.Fatalf("insertDocumentAsset(%q): %v", s3Key, err)
	}
}

func uploadObject(t *testing.T, client *awss3.Client, key string) {
	t.Helper()
	_, err := client.PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(key),
		Body:   strings.NewReader("test payload: " + key),
	})
	if err != nil {
		t.Fatalf("uploadObject(%q): %v", key, err)
	}
}

func listKeys(t *testing.T, client *awss3.Client, prefix string) []string {
	t.Helper()
	out, err := client.ListObjectsV2(context.Background(), &awss3.ListObjectsV2Input{
		Bucket: aws.String(testBucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		t.Fatalf("listKeys(%q): %v", prefix, err)
	}
	result := make([]string, 0, len(out.Contents))
	for _, obj := range out.Contents {
		result = append(result, aws.ToString(obj.Key))
	}
	return result
}

func makeConfig(t *testing.T, paths []string) *config.Config {
	t.Helper()
	return &config.Config{
		Database: config.DatabaseConfig{DSN: globalPGDSN},
		S3: config.S3Config{
			Endpoint: globalMinioURL, Region: "us-east-1", Bucket: testBucket,
			AccessKeyID: minioUser, SecretAccessKey: minioPassword,
			UsePathStyle: true, Paths: paths,
		},
		Cleaner: config.CleanerConfig{
			DBPageSize:        100,
			S3DeleteBatchSize: 1000,
			StateFile:         filepath.Join(t.TempDir(), "state.json"),
		},
	}
}

func runCleaner(t *testing.T, cfg *config.Config) error {
	t.Helper()
	ctx := context.Background()
	db := openDB()
	defer db.Close()
	s3Client, err := s3repo.NewClient(ctx, &cfg.S3)
	if err != nil {
		return fmt.Errorf("s3 client: %w", err)
	}
	return service.New(
		cfg,
		state.New(cfg.Cleaner.StateFile),
		postgres.NewAssetRepository(db),
		postgres.NewS3KeysRepository(db),
		s3repo.New(s3Client, &cfg.S3),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	).Run(ctx)
}

func TestIntegration_FullRun(t *testing.T) {
	t.Cleanup(func() { resetTestData(t) })
	ctx := context.Background()
	db := openDB()
	defer db.Close()
	s3c := buildS3Client(ctx)
	prefix := "fullrun/"

	refKey := prefix + "referenced.jpg"
	orphan1 := prefix + "orphan1.jpg"
	orphan2 := prefix + "orphan2.jpg"
	for _, k := range []string{refKey, orphan1, orphan2} {
		uploadObject(t, s3c, k)
	}
	insertImageAsset(t, db, refKey)

	if err := runCleaner(t, makeConfig(t, []string{prefix})); err != nil {
		t.Fatalf("RunCleaner: %v", err)
	}

	remaining := listKeys(t, s3c, prefix)
	t.Logf("remaining: %v", remaining)

	if !slices.Contains(remaining, refKey) {
		t.Errorf("referenced key %q was incorrectly deleted", refKey)
	}
	for _, orphan := range []string{orphan1, orphan2} {
		if slices.Contains(remaining, orphan) {
			t.Errorf("orphan %q was NOT deleted", orphan)
		}
	}
}

func TestIntegration_DryRun(t *testing.T) {
	t.Cleanup(func() { resetTestData(t) })
	ctx := context.Background()
	s3c := buildS3Client(ctx)
	prefix := "dryrun/"

	orphan := prefix + "should_stay.jpg"
	uploadObject(t, s3c, orphan)

	cfg := makeConfig(t, []string{prefix})
	cfg.Cleaner.DryRun = true
	if err := runCleaner(t, cfg); err != nil {
		t.Fatalf("RunCleaner: %v", err)
	}

	if !slices.Contains(listKeys(t, s3c, prefix), orphan) {
		t.Errorf("dry_run deleted %q — it should have been kept", orphan)
	}
}

func TestIntegration_MultiplePaths(t *testing.T) {
	t.Cleanup(func() { resetTestData(t) })
	ctx := context.Background()
	db := openDB()
	defer db.Close()
	s3c := buildS3Client(ctx)

	imgRef, imgOrphan := "mp-images/photo.jpg", "mp-images/old_photo.jpg"
	docRef, docOrphan := "mp-docs/report.pdf", "mp-docs/draft.pdf"
	for _, k := range []string{imgRef, imgOrphan, docRef, docOrphan} {
		uploadObject(t, s3c, k)
	}
	insertImageAsset(t, db, imgRef)
	insertDocumentAsset(t, db, docRef)

	if err := runCleaner(t, makeConfig(t, []string{"mp-images/", "mp-docs/"})); err != nil {
		t.Fatalf("RunCleaner: %v", err)
	}

	all := listKeys(t, s3c, "mp-")
	for _, want := range []string{imgRef, docRef} {
		if !slices.Contains(all, want) {
			t.Errorf("referenced %q was deleted", want)
		}
	}
	for _, gone := range []string{imgOrphan, docOrphan} {
		if slices.Contains(all, gone) {
			t.Errorf("orphan %q NOT deleted", gone)
		}
	}
}

func TestIntegration_Pagination(t *testing.T) {
	t.Cleanup(func() { resetTestData(t) })
	ctx := context.Background()
	db := openDB()
	defer db.Close()
	s3c := buildS3Client(ctx)
	prefix := "pagination/"

	var refKeys []string
	for i := range 25 {
		key := fmt.Sprintf("%simage_%03d.jpg", prefix, i)
		uploadObject(t, s3c, key)
		insertImageAsset(t, db, key)
		refKeys = append(refKeys, key)
	}
	orphan1, orphan2 := prefix+"orphan_a.jpg", prefix+"orphan_b.jpg"
	uploadObject(t, s3c, orphan1)
	uploadObject(t, s3c, orphan2)

	cfg := makeConfig(t, []string{prefix})
	cfg.Cleaner.DBPageSize = 5

	if err := runCleaner(t, cfg); err != nil {
		t.Fatalf("RunCleaner: %v", err)
	}

	remaining := listKeys(t, s3c, prefix)
	t.Logf("remaining: %d objects", len(remaining))

	for _, k := range refKeys {
		if !slices.Contains(remaining, k) {
			t.Errorf("referenced key %q deleted", k)
		}
	}
	for _, orphan := range []string{orphan1, orphan2} {
		if slices.Contains(remaining, orphan) {
			t.Errorf("orphan %q NOT deleted", orphan)
		}
	}
}

func TestIntegration_ResumeFromCheckpoint(t *testing.T) {
	t.Cleanup(func() { resetTestData(t) })
	ctx := context.Background()
	db := openDB()
	defer db.Close()
	s3c := buildS3Client(ctx)
	prefix := "resume/"

	refKey := prefix + "kept.jpg"
	orphan := prefix + "gone.jpg"
	uploadObject(t, s3c, refKey)
	uploadObject(t, s3c, orphan)
	insertImageAsset(t, db, refKey)

	cfg := makeConfig(t, []string{prefix})

	if err := runCleaner(t, cfg); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if slices.Contains(listKeys(t, s3c, prefix), orphan) {
		t.Fatal("setup: orphan should be deleted after first run")
	}

	newOrphan := prefix + "new_orphan.jpg"
	uploadObject(t, s3c, newOrphan)

	if err := runCleaner(t, cfg); err != nil {
		t.Fatalf("second (no-op) run: %v", err)
	}

	if !slices.Contains(listKeys(t, s3c, prefix), newOrphan) {
		t.Logf("new orphan was cleaned — this means the run was NOT a no-op (state was not reused)")
	} else {
		t.Logf("confirmed: second run was a no-op (state reused, new orphan untouched)")
	}

	if !slices.Contains(listKeys(t, s3c, prefix), refKey) {
		t.Errorf("refKey %q was deleted", refKey)
	}
}
