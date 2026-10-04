package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database DatabaseConfig `yaml:"database"`
	S3       S3Config       `yaml:"s3"`
	Cleaner  CleanerConfig  `yaml:"cleaner"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type S3Config struct {
	Endpoint        string   `yaml:"endpoint"`
	Region          string   `yaml:"region"`
	Bucket          string   `yaml:"bucket"`
	AccessKeyID     string   `yaml:"access_key_id"`
	SecretAccessKey string   `yaml:"secret_access_key"`
	UsePathStyle    bool     `yaml:"use_path_style"`
	Paths           []string `yaml:"paths"`
	OlderThan       string   `yaml:"older_than"`
}

type CleanerConfig struct {
	DBPageSize        int    `yaml:"db_page_size"`
	S3DeleteBatchSize int    `yaml:"s3_delete_batch_size"`
	StateFile         string `yaml:"state_file"`
	DryRun            bool   `yaml:"dry_run"`
	StashFile         string `yaml:"stash_file"`
	MinDBAssets       int64  `yaml:"min_db_assets"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}
func (c *Config) applyDefaults() {
	if c.S3.Region == "" {
		c.S3.Region = "us-east-1"
	}
	if c.Cleaner.DBPageSize <= 0 {
		c.Cleaner.DBPageSize = 1000
	}
	if c.Cleaner.S3DeleteBatchSize <= 0 || c.Cleaner.S3DeleteBatchSize > 1000 {
		c.Cleaner.S3DeleteBatchSize = 1000 // AWS hard limit
	}
	if c.Cleaner.StateFile == "" {
		c.Cleaner.StateFile = "state.json"
	}
}
func (c *Config) validate() error {
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.S3.Bucket == "" {
		return fmt.Errorf("s3.bucket is required")
	}
	if len(c.S3.Paths) == 0 {
		return fmt.Errorf("s3.paths must not be empty")
	}
	return nil
}
