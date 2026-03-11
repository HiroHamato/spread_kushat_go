package httpserver

import (
	"encoding/json"
	"net/http"

	"spread_kushat_pora_golang/internal/domain/entity"
)

type SnapshotReader interface {
	Snapshot() entity.WatcherSnapshot
}

func New(addr string, watcher SnapshotReader, telegramEnabled bool) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := watcher.Snapshot()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"bot":       telegramEnabled,
			"updatedAt": snap.UpdatedAt,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Spread bot is running. Use Telegram /menu."))
	})

	return &http.Server{Addr: addr, Handler: mux}
}
