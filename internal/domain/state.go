package domain

import (
	"time"
)

type Phase int

const (
	PhaseS3Scan   Phase = 1
	PhaseDBScan   Phase = 2
	PhaseDelete   Phase = 3
	PhaseComplete Phase = 4
)

type State struct {
	Phase          Phase     `json:"phase"`
	S3ScanDone     bool      `json:"s3_scan_done"`
	DBScanDone     bool      `json:"db_scan_done"`
	DeleteDone     bool      `json:"delete_done"`
	DBLastID       string    `json:"db_last_id"`
	DBRowsScanned  int64     `json:"db_rows_scanned"`
	S3KeysFound    int64     `json:"s3_keys_found"`
	DeletedCount   int64     `json:"deleted_count"`
	ProcessedPaths []string  `json:"processed_paths"`
	ParseErrCount  int64     `json:"parse_err_count"`
	StartedAt      time.Time `json:"started_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func NewState() *State {
	now := time.Now().UTC()
	return &State{
		Phase:     PhaseS3Scan,
		StartedAt: now,
		UpdatedAt: now,
	}
}
func (s *State) Touch() {
	s.UpdatedAt = time.Now().UTC()

}
