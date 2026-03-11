package repository

import (
	"context"

	"spread_kushat_pora_golang/internal/domain/entity"
)

type SessionRepository interface {
	GetOrCreate(ctx context.Context, chatID string) (*entity.Session, error)
	Save(ctx context.Context, session *entity.Session) error
	List(ctx context.Context) ([]*entity.Session, error)
	ListTracked(ctx context.Context, chatID string) ([]entity.TrackedItem, error)
	UpsertTracked(ctx context.Context, chatID string, item entity.TrackedItem) error
	DeleteTracked(ctx context.Context, chatID string, id string) error
	ClearTracked(ctx context.Context, chatID string) error
}

type StateRepository interface {
	SaveSnapshot(ctx context.Context, snapshot entity.WatcherSnapshot) error
	LoadSnapshot(ctx context.Context) (entity.WatcherSnapshot, error)
	AppendSpreadHistory(ctx context.Context, historyKey string, point entity.SpreadPoint, limit int) error
	GetSpreadHistory(ctx context.Context, historyKey string) ([]entity.SpreadPoint, error)
	SetHistoryLastSeen(ctx context.Context, historyKey string, ts int64) error
	GetHistoryLastSeen(ctx context.Context) (map[string]int64, error)
	DeleteSpreadHistory(ctx context.Context, historyKey string) error
	AppendPriceHistory(ctx context.Context, key string, point entity.PricePoint, limit int) error
	GetPriceHistoryByPair(ctx context.Context, pair string) (map[string][]entity.PricePoint, error)
}
