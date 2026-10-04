package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

type imageProps struct {
	Image *struct {
		Renditions *struct {
			Small    string `json:"small"`
			Original string `json:"original"`
			Medium   string `json:"medium"`
		} `json:"renditions"`
	} `json:"image"`
	StorageKey string `json:"storageKey"`
}

type documentProps struct {
	Document *struct {
		SourceKey string `json:"sourceKey"`
		Pages     []struct {
			Key        string `json:"key"`
			Renditions *struct {
				Small  string `json:"small"`
				Medium string `json:"medium"`
			} `json:"renditions"`
		} `json:"pages"`
	} `json:"document"`
}

func ExtractS3Keys(e Asset) ([]string, error) {
	if !e.IsMediaType() {
		return nil, nil
	}
	switch AssetKind(e.Kind) {
	case AssetKindImage:
		return extractImageKeys(e.Metadata)
	case AssetKindDocument, AssetKindPDF, AssetKindPresentation, AssetKindSpreadsheet:
		return extractDocumentKeys(e.Metadata)

	}
	return nil, nil
}

func extractImageKeys(raw json.RawMessage) ([]string, error) {
	var props imageProps
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, fmt.Errorf("parse image metadata: %w", err)
	}
	var keys []string
	if props.StorageKey != "" {
		keys = append(keys, props.StorageKey)
	}
	if props.Image != nil && props.Image.Renditions != nil {
		q := props.Image.Renditions
		keys = appendNonEmpty(keys, q.Small, q.Original, q.Medium)
	}
	return keys, nil
}

func extractDocumentKeys(raw json.RawMessage) ([]string, error) {
	var props documentProps
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, fmt.Errorf("parse document metadata: %w", err)
	}
	if props.Document == nil {
		return nil, nil
	}
	var keys []string
	keys = appendNonEmpty(keys, props.Document.SourceKey)
	for _, page := range props.Document.Pages {
		keys = appendNonEmpty(keys, page.Key)
		if page.Renditions != nil {
			keys = appendNonEmpty(keys, page.Renditions.Small, page.Renditions.Medium)
		}
	}
	return keys, nil
}

func appendNonEmpty(dst []string, values ...string) []string {
	for _, v := range values {
		if v != "" {
			dst = append(dst, v)
		}
	}
	return dst
}

func NormaliseKey(s string) string {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return s
	}
	parts := strings.SplitN(s, "/", 4)
	if len(parts) == 4 {
		return parts[3]
	}
	return s
}
