package dictionary

import (
	"context"

	"english-learning-mcp/internal/domain"
)

type Provider interface {
	Name() string
	ParserVersion() int
	DatasetVersion() string
	Lookup(ctx context.Context, term string) (domain.DictionarySnapshotData, error)
}
