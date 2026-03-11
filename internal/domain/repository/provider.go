package repository

import (
	"context"

	"spread_kushat_pora_golang/internal/domain/entity"
)

type QuoteProvider interface {
	GetQuotes(ctx context.Context, mode string) ([]entity.Quote, error)
}
