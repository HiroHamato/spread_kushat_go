package watcher

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"spread_kushat_pora_golang/internal/config"
	"spread_kushat_pora_golang/internal/domain/entity"
	"spread_kushat_pora_golang/internal/domain/repository"
	"spread_kushat_pora_golang/internal/domain/service"
)

const (
	historyLimit      = 600
	priceHistoryLimit = 800
	historyStaleMS    = int64(3 * 60 * 60 * 1000)
	cleanupEvery      = 30
)

type AlertHandler interface {
	HandleOpportunities(ctx context.Context, opportunities []entity.Opportunity, now int64)
}

type Service struct {
	cfg       config.Config
	provider  repository.QuoteProvider
	stateRepo repository.StateRepository
	alerts    AlertHandler

	mu sync.RWMutex

	updatedAt     int64
	opportunities []entity.Opportunity
	history       map[string][]entity.SpreadPoint
	priceHistory  map[string][]entity.PricePoint
	historySeen   map[string]int64
	cycles        int
}

func NewService(cfg config.Config, provider repository.QuoteProvider, stateRepo repository.StateRepository, alerts AlertHandler) *Service {
	return &Service{
		cfg:          cfg,
		provider:     provider,
		stateRepo:    stateRepo,
		alerts:       alerts,
		history:      make(map[string][]entity.SpreadPoint),
		priceHistory: make(map[string][]entity.PricePoint),
		historySeen:  make(map[string]int64),
	}
}

func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.RefreshMS) * time.Millisecond)
	defer ticker.Stop()

	s.processOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processOnce(ctx)
		}
	}
}

func (s *Service) processOnce(ctx context.Context) {
	quotes, err := s.provider.GetQuotes(ctx, s.cfg.DefaultMode)
	if err != nil {
		return
	}
	if len(quotes) == 0 {
		return
	}
	now := time.Now().UnixMilli()
	opps := s.buildOpportunities(quotes, now)

	s.mu.Lock()
	s.updatedAt = now
	s.opportunities = opps
	s.updatePriceHistoryLocked(quotes, now)
	s.updateSpreadHistoryLocked(ctx, opps, now)
	s.cleanupHistoryLocked(ctx, now)
	snapshot := entity.WatcherSnapshot{UpdatedAt: now, Opportunities: opps}
	s.mu.Unlock()

	if s.stateRepo != nil {
		_ = s.stateRepo.SaveSnapshot(ctx, snapshot)
	}
	if s.alerts != nil {
		s.alerts.HandleOpportunities(ctx, opps, now)
	}
}

func (s *Service) buildOpportunities(quotes []entity.Quote, now int64) []entity.Opportunity {
	byPairMarket := service.GroupByPairMarket(quotes)
	byPair := service.GroupByPair(quotes)
	out := make([]entity.Opportunity, 0, 2048)
	fees := service.FeeConfig{
		TradingPercentPerSide: s.cfg.TradingFeePerSide,
		WithdrawPercent:       s.cfg.WithdrawFeePercent,
		NetworkPercent:        s.cfg.NetworkFeePercent,
	}

	for key, rows := range byPairMarket {
		if len(rows) < 2 {
			continue
		}
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		pair := parts[0]
		market := parts[1]
		s.collectDirection(&out, rows, rows, pair, market, market, market, now, fees)
	}

	for pair, rows := range byPair {
		spotRows := service.FilterMarket(rows, "spot")
		futuresRows := service.FilterMarket(rows, "futures")
		if len(spotRows) == 0 || len(futuresRows) == 0 {
			continue
		}
		s.collectDirection(&out, spotRows, futuresRows, pair, "spot-futures", "spot", "futures", now, fees)
		s.collectDirection(&out, futuresRows, spotRows, pair, "spot-futures", "futures", "spot", now, fees)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].NetSpreadPercent > out[j].NetSpreadPercent
	})
	return out
}

func (s *Service) collectDirection(out *[]entity.Opportunity, a, b []entity.Quote, pair, market, buyMarket, sellMarket string, now int64, fees service.FeeConfig) {
	for _, buy := range a {
		for _, sell := range b {
			op := service.BuildOpportunity(pair, market, buyMarket, sellMarket, buy, sell, now, s.cfg.MinSpreadPercent, fees)
			if op == nil {
				continue
			}
			*out = append(*out, *op)
		}
	}
}

func (s *Service) updatePriceHistoryLocked(quotes []entity.Quote, now int64) {
	for _, q := range quotes {
		key := q.Symbol + "|" + q.Exchange + "|" + q.Market
		mid := (q.Bid + q.Ask) / 2
		rows := append(s.priceHistory[key], entity.PricePoint{TS: now, Price: mid})
		if len(rows) > priceHistoryLimit {
			rows = rows[len(rows)-priceHistoryLimit:]
		}
		s.priceHistory[key] = rows
		if s.stateRepo != nil {
			_ = s.stateRepo.AppendPriceHistory(context.Background(), key, entity.PricePoint{TS: now, Price: mid}, priceHistoryLimit)
		}
	}
}

func (s *Service) updateSpreadHistoryLocked(ctx context.Context, opportunities []entity.Opportunity, now int64) {
	for _, op := range opportunities {
		point := entity.SpreadPoint{TS: now, SpreadPercent: op.SpreadPercent, NetSpreadPercent: op.NetSpreadPercent}
		rows := append(s.history[op.HistoryKey], point)
		if len(rows) > historyLimit {
			rows = rows[len(rows)-historyLimit:]
		}
		s.history[op.HistoryKey] = rows
		s.historySeen[op.HistoryKey] = now
		if s.stateRepo != nil {
			_ = s.stateRepo.AppendSpreadHistory(ctx, op.HistoryKey, point, historyLimit)
			_ = s.stateRepo.SetHistoryLastSeen(ctx, op.HistoryKey, now)
		}
	}
}

func (s *Service) cleanupHistoryLocked(ctx context.Context, now int64) {
	s.cycles++
	if s.cycles%cleanupEvery != 0 {
		return
	}
	staleBefore := now - historyStaleMS
	for key, seen := range s.historySeen {
		if seen >= staleBefore {
			continue
		}
		delete(s.historySeen, key)
		delete(s.history, key)
		if s.stateRepo != nil {
			_ = s.stateRepo.DeleteSpreadHistory(ctx, key)
		}
	}
}

func (s *Service) Snapshot() entity.WatcherSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyRows := make([]entity.Opportunity, len(s.opportunities))
	copy(copyRows, s.opportunities)
	return entity.WatcherSnapshot{UpdatedAt: s.updatedAt, Opportunities: copyRows}
}

func (s *Service) SpreadTrendForOpportunity(op entity.Opportunity, now int64) entity.Trend {
	if op.HistoryKey == "" {
		return entity.Trend{State: "insufficient", CurrentPercent: op.SpreadPercent}
	}
	rows := s.GetSpreadHistoryByKey(op.HistoryKey)
	return service.AnalyzeHistory(rows, now)
}

func (s *Service) GetSpreadHistoryByKey(historyKey string) []entity.SpreadPoint {
	s.mu.RLock()
	rows := append([]entity.SpreadPoint(nil), s.history[historyKey]...)
	s.mu.RUnlock()
	if len(rows) > 0 {
		return rows
	}
	if s.stateRepo == nil {
		return []entity.SpreadPoint{}
	}
	loaded, err := s.stateRepo.GetSpreadHistory(context.Background(), historyKey)
	if err != nil {
		return []entity.SpreadPoint{}
	}
	return loaded
}

func (s *Service) GetPriceHistoryByPair(pair string) map[string][]entity.PricePoint {
	s.mu.RLock()
	out := map[string][]entity.PricePoint{}
	prefix := pair + "|"
	for key, rows := range s.priceHistory {
		if strings.HasPrefix(key, prefix) {
			copyRows := append([]entity.PricePoint(nil), rows...)
			out[key] = copyRows
		}
	}
	s.mu.RUnlock()
	if len(out) > 0 || s.stateRepo == nil {
		return out
	}
	loaded, err := s.stateRepo.GetPriceHistoryByPair(context.Background(), pair)
	if err != nil {
		return map[string][]entity.PricePoint{}
	}
	return loaded
}
