package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/denagava/s3-orphan-cleaner/internal/config"
	"github.com/denagava/s3-orphan-cleaner/internal/repository/postgres"
	s3repo "github.com/denagava/s3-orphan-cleaner/internal/repository/s3"
	"github.com/denagava/s3-orphan-cleaner/internal/service"
	"github.com/denagava/s3-orphan-cleaner/internal/state"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfgPath := "config.yaml"
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		cfgPath = v
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	logger.Info("s3-orphan-cleaner starting",
		"dry_run", cfg.Cleaner.DryRun,
		"s3_paths", cfg.S3.Paths,
		"db_page_size", cfg.Cleaner.DBPageSize,
	)

	db := postgres.OpenDB(cfg.Database.DSN)
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	logger.Info("database connected")

	stateMgr := state.New(cfg.Cleaner.StateFile)
	assetRepo := postgres.NewAssetRepository(db)
	keysRepo := postgres.NewS3KeysRepository(db)

	s3Client, err := s3repo.NewClient(ctx, &cfg.S3)
	if err != nil {
		return fmt.Errorf("create s3 client: %w", err)
	}
	logger.Info("s3 client ready",
		"bucket", cfg.S3.Bucket,
		"endpoint", cfg.S3.Endpoint,
	)
	s3Repo := s3repo.New(s3Client, &cfg.S3)

	svc := service.New(cfg, stateMgr, assetRepo, keysRepo, s3Repo, logger)
	return svc.Run(ctx)
}
