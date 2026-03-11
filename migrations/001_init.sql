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
