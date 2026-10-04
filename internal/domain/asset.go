package domain

import (
	"encoding/json"

	"github.com/google/uuid"
)

type AssetKind string

const (
	AssetKindImage        AssetKind = "IMAGE"
	AssetKindDocument     AssetKind = "DOCUMENT"
	AssetKindPDF          AssetKind = "PDF"
	AssetKindPresentation AssetKind = "PRESENTATION"
	AssetKindSpreadsheet  AssetKind = "SPREADSHEET"
)

var mediaTypes = map[AssetKind]struct{}{
	AssetKindImage:        {},
	AssetKindDocument:     {},
	AssetKindPDF:          {},
	AssetKindPresentation: {},
	AssetKindSpreadsheet:  {},
}

type Asset struct {
	ID       uuid.UUID
	Kind     string
	Metadata json.RawMessage
}

func (e Asset) IsMediaType() bool {
	_, ok := mediaTypes[AssetKind(e.Kind)]
	return ok

}
