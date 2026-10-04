package domain_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/denagava/s3-orphan-cleaner/internal/domain"
)

func newAsset(kind, props string) domain.Asset {
	return domain.Asset{
		ID:       uuid.New(),
		Kind:     kind,
		Metadata: json.RawMessage(props),
	}
}

func TestExtractS3Keys(t *testing.T) {
	tests := []struct {
		name     string
		asset    domain.Asset
		wantKeys []string
		wantErr  bool
	}{
		// ── IMAGE ──────────────────────────────────────────────────────────
		{
			name:     "image/full_renditions",
			asset:    newAsset("IMAGE", `{"image":{"renditions":{"small":"img/low.jpg","original":"img/orig.jpg","medium":"img/prev.jpg"}}}`),
			wantKeys: []string{"img/low.jpg", "img/orig.jpg", "img/prev.jpg"},
		},
		{
			name:     "image/legacy_only",
			asset:    newAsset("IMAGE", `{"storageKey":"img/legacy.jpg"}`),
			wantKeys: []string{"img/legacy.jpg"},
		},
		{
			name:     "image/legacy_and_renditions",
			asset:    newAsset("IMAGE", `{"storageKey":"old.jpg","image":{"renditions":{"small":"new_low.jpg","original":"new_orig.jpg"}}}`),
			wantKeys: []string{"old.jpg", "new_low.jpg", "new_orig.jpg"},
		},
		{
			name:     "image/partial_renditions",
			asset:    newAsset("IMAGE", `{"image":{"renditions":{"original":"only_original.jpg"}}}`),
			wantKeys: []string{"only_original.jpg"},
		},
		{
			name:     "image/empty_renditions",
			asset:    newAsset("IMAGE", `{"image":{"renditions":{}}}`),
			wantKeys: nil,
		},
		{
			name:     "image/null_image",
			asset:    newAsset("IMAGE", `{}`),
			wantKeys: nil,
		},

		{
			name: "document/with_pages",
			asset: newAsset("DOCUMENT", `{
				"document":{
					"sourceKey":"docs/file.pdf",
					"pages":[
						{"key":"docs/p1.jpg","renditions":{"small":"docs/p1_low.jpg","medium":"docs/p1_prev.jpg"}},
						{"key":"docs/p2.jpg"}
					]
				}
			}`),
			wantKeys: []string{"docs/file.pdf", "docs/p1.jpg", "docs/p1_low.jpg", "docs/p1_prev.jpg", "docs/p2.jpg"},
		},
		{
			name:     "document/no_pages",
			asset:    newAsset("DOCUMENT", `{"document":{"sourceKey":"docs/solo.pdf"}}`),
			wantKeys: []string{"docs/solo.pdf"},
		},
		{
			name:     "document/no_document_field",
			asset:    newAsset("DOCUMENT", `{}`),
			wantKeys: nil,
		},
		{
			name:     "pdf/basic",
			asset:    newAsset("PDF", `{"document":{"sourceKey":"files/report.pdf"}}`),
			wantKeys: []string{"files/report.pdf"},
		},
		{
			name:     "presentation/basic",
			asset:    newAsset("PRESENTATION", `{"document":{"sourceKey":"files/deck.ppt"}}`),
			wantKeys: []string{"files/deck.ppt"},
		},
		{
			name:     "spreadsheet/basic",
			asset:    newAsset("SPREADSHEET", `{"document":{"sourceKey":"files/sheet.xlsx"}}`),
			wantKeys: []string{"files/sheet.xlsx"},
		},

		{
			name:     "non_media_type",
			asset:    newAsset("TEXT", `{"text":"hello world"}`),
			wantKeys: nil,
		},
		{
			name:     "unknown_type",
			asset:    newAsset("WIDGET", `{"foo":"bar"}`),
			wantKeys: nil,
		},
		{
			name:     "empty_type",
			asset:    newAsset("", `{}`),
			wantKeys: nil,
		},
		{
			name:    "invalid_json",
			asset:   newAsset("IMAGE", `{not valid json`),
			wantErr: true,
		},
		{
			name:     "empty_metadata",
			asset:    newAsset("IMAGE", `{}`),
			wantKeys: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ExtractS3Keys(tc.asset)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tc.wantKeys) {
				t.Errorf("\ngot %v\nwant %v", got, tc.wantKeys)
			}
		})
	}
}

func TestNormaliseKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"uploads/img.jpg", "uploads/img.jpg"},
		{"img.jpg", "img.jpg"},
		{"", ""},

		{"https://bucket.s3.amazonaws.com/uploads/img.jpg", "uploads/img.jpg"},
		{"https://bucket.s3.eu-west-1.amazonaws.com/a/b/c.jpg", "a/b/c.jpg"},

		{"http://localhost:9000/media/uploads/img.jpg", "media/uploads/img.jpg"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := domain.NormaliseKey(tc.input)
			if got != tc.want {
				t.Errorf("NormaliseKey(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
