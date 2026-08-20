package dashboard

import (
	"log/slog"
	"net/http"
)

// NewHandler returns the complete read-only dashboard and API handler.
func NewHandler(store Store, cfg Config, health HealthProvider, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	Register(mux, store, cfg, health, logger)
	return mux
}

// Register adds dashboard routes to an existing application mux.
func Register(mux *http.ServeMux, store Store, cfg Config, health HealthProvider, logger *slog.Logger) {
	mux.HandleFunc("GET /api/jobs", (feedServer{store: store, totalSources: cfg.TotalSources, logoDomains: cfg.LogoDomains, logger: logger}).handler)
	mux.HandleFunc("GET /api/status", (statusServer{store: store, health: health, config: cfg, logger: logger}).handler)
	registerUI(mux)
}

func newServerHandler(_ HealthProvider, store Store, cfg Config, logger *slog.Logger) http.Handler {
	return NewHandler(store, cfg, nil, logger)
}
