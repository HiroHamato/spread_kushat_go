package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"spread_kushat_pora_golang/internal/domain/entity"
)

type SessionRepository struct {
	pool *pgxpool.Pool
}

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Migration is intentionally simple and idempotent for single-service deployment.
	query := `
CREATE TABLE IF NOT EXISTS sessions (
    chat_id TEXT PRIMARY KEY,
    modes TEXT[] NOT NULL DEFAULT ARRAY['spot-futures'],
    min_net_spread DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    min_volume DOUBLE PRECISION NOT NULL DEFAULT 0,
    page INTEGER NOT NULL DEFAULT 0,
    ui_screen TEXT NOT NULL DEFAULT 'main',
    alert_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    alert_threshold DOUBLE PRECISION NOT NULL DEFAULT 1,
    alert_spike_min_vol DOUBLE PRECISION NOT NULL DEFAULT 0,
    alert_spike_modes TEXT[] NOT NULL DEFAULT ARRAY['spot-futures','spot','futures'],
    menu_message_id BIGINT,
    last_rendered_state TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tracked_items (
    chat_id TEXT NOT NULL,
    opportunity_id TEXT NOT NULL,
    title TEXT NOT NULL,
    pair TEXT NOT NULL,
    market TEXT NOT NULL,
    buy_exchange TEXT NOT NULL,
    buy_market TEXT NOT NULL,
    sell_exchange TEXT NOT NULL,
    sell_market TEXT NOT NULL,
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (chat_id, opportunity_id),
    CONSTRAINT fk_tracked_session FOREIGN KEY (chat_id) REFERENCES sessions(chat_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tracked_items_chat_id ON tracked_items(chat_id);
`
	_, err := pool.Exec(ctx, query)
	return err
}

func (r *SessionRepository) GetOrCreate(ctx context.Context, chatID string) (*entity.Session, error) {
	if err := r.ensureExists(ctx, chatID); err != nil {
		return nil, err
	}
	return r.get(ctx, chatID)
}

func (r *SessionRepository) ensureExists(ctx context.Context, chatID string) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO sessions (
  chat_id, modes, min_net_spread, min_volume, page, ui_screen, alert_enabled, alert_threshold, alert_spike_min_vol, alert_spike_modes, last_rendered_state
) VALUES ($1, ARRAY['spot-futures'], 0.5, 0, 0, 'main', false, 1, 0, ARRAY['spot-futures','spot','futures'], '')
ON CONFLICT (chat_id) DO NOTHING
`, chatID)
	return err
}

func (r *SessionRepository) get(ctx context.Context, chatID string) (*entity.Session, error) {
	var session entity.Session
	var modes []string
	var spikeModes []string
	var menuMessageID *int64
	err := r.pool.QueryRow(ctx, `
SELECT chat_id, modes, min_net_spread, min_volume, page, ui_screen, alert_enabled, alert_threshold, alert_spike_min_vol, alert_spike_modes, menu_message_id, last_rendered_state
FROM sessions
WHERE chat_id = $1
`, chatID).Scan(
		&session.ChatID,
		&modes,
		&session.MinNetSpread,
		&session.MinVolume,
		&session.Page,
		&session.UIScreen,
		&session.AlertEnabled,
		&session.AlertThreshold,
		&session.AlertSpikeMinVol,
		&spikeModes,
		&menuMessageID,
		&session.LastRenderedState,
	)
	if err != nil {
		return nil, err
	}
	session.Modes = modes
	session.AlertSpikeModes = spikeModes
	session.MenuMessageID = menuMessageID
	return &session, nil
}

func (r *SessionRepository) Save(ctx context.Context, session *entity.Session) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}
	_, err := r.pool.Exec(ctx, `
UPDATE sessions
SET modes = $2,
    min_net_spread = $3,
    min_volume = $4,
    page = $5,
    ui_screen = $6,
    alert_enabled = $7,
    alert_threshold = $8,
    alert_spike_min_vol = $9,
    alert_spike_modes = $10,
    menu_message_id = $11,
    last_rendered_state = $12,
    updated_at = NOW()
WHERE chat_id = $1
`,
		session.ChatID,
		session.Modes,
		session.MinNetSpread,
		session.MinVolume,
		session.Page,
		session.UIScreen,
		session.AlertEnabled,
		session.AlertThreshold,
		session.AlertSpikeMinVol,
		session.AlertSpikeModes,
		session.MenuMessageID,
		session.LastRenderedState,
	)
	return err
}

func (r *SessionRepository) List(ctx context.Context) ([]*entity.Session, error) {
	rows, err := r.pool.Query(ctx, `
SELECT chat_id, modes, min_net_spread, min_volume, page, ui_screen, alert_enabled, alert_threshold, alert_spike_min_vol, alert_spike_modes, menu_message_id, last_rendered_state
FROM sessions
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*entity.Session, 0)
	for rows.Next() {
		var s entity.Session
		var modes []string
		var spike []string
		var menuMessageID *int64
		if err := rows.Scan(
			&s.ChatID,
			&modes,
			&s.MinNetSpread,
			&s.MinVolume,
			&s.Page,
			&s.UIScreen,
			&s.AlertEnabled,
			&s.AlertThreshold,
			&s.AlertSpikeMinVol,
			&spike,
			&menuMessageID,
			&s.LastRenderedState,
		); err != nil {
			return nil, err
		}
		s.Modes = modes
		s.AlertSpikeModes = spike
		s.MenuMessageID = menuMessageID
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (r *SessionRepository) ListTracked(ctx context.Context, chatID string) ([]entity.TrackedItem, error) {
	rows, err := r.pool.Query(ctx, `
SELECT opportunity_id, title, pair, market, buy_exchange, buy_market, sell_exchange, sell_market, created_at_ms
FROM tracked_items
WHERE chat_id = $1
ORDER BY created_at_ms ASC
`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]entity.TrackedItem, 0)
	for rows.Next() {
		var item entity.TrackedItem
		item.ID = ""
		if err := rows.Scan(
			&item.ID,
			&item.Title,
			&item.Pair,
			&item.Market,
			&item.BuyExchange,
			&item.BuyMarket,
			&item.SellExchange,
			&item.SellMarket,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SessionRepository) UpsertTracked(ctx context.Context, chatID string, item entity.TrackedItem) error {
	if err := r.ensureExists(ctx, chatID); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO tracked_items (
  chat_id, opportunity_id, title, pair, market, buy_exchange, buy_market, sell_exchange, sell_market, created_at_ms
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (chat_id, opportunity_id)
DO UPDATE SET
  title = EXCLUDED.title,
  pair = EXCLUDED.pair,
  market = EXCLUDED.market,
  buy_exchange = EXCLUDED.buy_exchange,
  buy_market = EXCLUDED.buy_market,
  sell_exchange = EXCLUDED.sell_exchange,
  sell_market = EXCLUDED.sell_market,
  created_at_ms = EXCLUDED.created_at_ms
`,
		chatID,
		item.ID,
		item.Title,
		item.Pair,
		item.Market,
		item.BuyExchange,
		item.BuyMarket,
		item.SellExchange,
		item.SellMarket,
		item.CreatedAt,
	)
	return err
}

func (r *SessionRepository) DeleteTracked(ctx context.Context, chatID string, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM tracked_items WHERE chat_id = $1 AND opportunity_id = $2`, chatID, id)
	return err
}

func (r *SessionRepository) ClearTracked(ctx context.Context, chatID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM tracked_items WHERE chat_id = $1`, chatID)
	return err
}
