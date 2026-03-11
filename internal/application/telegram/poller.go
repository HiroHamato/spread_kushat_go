package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"spread_kushat_pora_golang/internal/config"
	"spread_kushat_pora_golang/internal/domain/entity"
	"spread_kushat_pora_golang/internal/domain/repository"
)

const (
	pageSize = 8
)

var (
	alertThresholds         = []float64{0.5, 1, 2, 5}
	modeOptions             = []string{"spot-futures", "spot", "futures"}
	alertSpikeVolumeOptions = []float64{0, 100000, 500000, 1000000, 50000000, 100000000}
)

type WatcherReader interface {
	Snapshot() entity.WatcherSnapshot
	SpreadTrendForOpportunity(op entity.Opportunity, now int64) entity.Trend
}

type Poller struct {
	cfg      config.Config
	sessions repository.SessionRepository
	watcher  WatcherReader
	alerts   *AlertManager
	http     *http.Client

	mu              sync.Mutex
	offset          int64
	running         bool
	sessionByChatID map[string]*runtimeSession
	oppCache        opportunityCache
}

type runtimeSession struct {
	mu sync.Mutex

	ChatID       string
	Modes        []string
	MinNetSpread float64
	MinVolume    float64
	Page         int

	UIScreen string

	AlertEnabled     bool
	AlertThreshold   float64
	AlertSpikeMinVol float64
	AlertSpikeModes  []string

	Tracked           map[string]entity.TrackedItem
	MenuMessageID     *int64
	LastRenderedState string
	SyncInFlight      bool
	SyncQueued        bool
}

type opportunityCache struct {
	UpdatedAt int64
	Rows      []entity.Opportunity
	ByID      map[string]entity.Opportunity
}

type telegramAPIResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
	CallbackQuery *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message *struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

type trackedViewItem struct {
	Item        entity.TrackedItem
	Opportunity *entity.Opportunity
}

type rowsState struct {
	Rows       []entity.Opportunity
	Total      int
	TotalPages int
	Page       int
}

type view struct {
	Text        string
	ReplyMarkup map[string]any
}

type sessionSnapshot struct {
	ChatID            string
	Modes             []string
	MinNetSpread      float64
	MinVolume         float64
	Page              int
	UIScreen          string
	AlertEnabled      bool
	AlertThreshold    float64
	AlertSpikeMinVol  float64
	AlertSpikeModes   []string
	Tracked           map[string]entity.TrackedItem
	MenuMessageID     *int64
	LastRenderedState string
}

func NewPoller(cfg config.Config, sessions repository.SessionRepository, watcher WatcherReader, alerts *AlertManager) *Poller {
	return &Poller{
		cfg:             cfg,
		sessions:        sessions,
		watcher:         watcher,
		alerts:          alerts,
		http:            &http.Client{Timeout: 35 * time.Second},
		sessionByChatID: make(map[string]*runtimeSession),
		oppCache:        opportunityCache{ByID: map[string]entity.Opportunity{}},
	}
}

func (p *Poller) Start(ctx context.Context) {
	if p.cfg.Telegram.BotToken == "" {
		return
	}

	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()

	go p.menuRefreshLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates := p.getUpdates(ctx)
		for _, upd := range updates {
			if upd.UpdateID >= p.offset {
				p.offset = upd.UpdateID + 1
			}
			p.handleUpdate(ctx, upd)
		}
	}
}

func (p *Poller) menuRefreshLoop(ctx context.Context) {
	interval := time.Duration(maxFloat(1000, float64(p.cfg.TGMenuRefreshMS))) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refreshOpenMenus(ctx)
		}
	}
}

func (p *Poller) getUpdates(ctx context.Context) []tgUpdate {
	payload := map[string]any{
		"offset":          p.offset,
		"timeout":         25,
		"allowed_updates": []string{"message", "callback_query"},
	}
	resp := p.botAPI(ctx, "getUpdates", payload)
	if !resp.OK {
		time.Sleep(1200 * time.Millisecond)
		return nil
	}
	var updates []tgUpdate
	if err := json.Unmarshal(resp.Result, &updates); err != nil {
		return nil
	}
	return updates
}

func (p *Poller) handleUpdate(ctx context.Context, update tgUpdate) {
	if update.Message != nil {
		chatID := strconv.FormatInt(update.Message.Chat.ID, 10)
		text := strings.TrimSpace(update.Message.Text)
		if text == "/start" || text == "/menu" || text == "/filters" {
			p.sendMenu(ctx, chatID)
			return
		}
		_ = p.botAPI(ctx, "sendMessage", map[string]any{
			"chat_id": chatID,
			"text":    "Use /menu to open spread board with inline filters.",
		})
		return
	}

	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		chatID := strconv.FormatInt(update.CallbackQuery.Message.Chat.ID, 10)
		messageID := update.CallbackQuery.Message.MessageID
		s := p.sessionFor(ctx, chatID)
		if s == nil {
			return
		}
		p.applyCallback(chatID, s, update.CallbackQuery.Data)
		_ = p.botAPI(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": update.CallbackQuery.ID})
		p.editMenu(ctx, chatID, messageID, nil)
	}
}

func (p *Poller) sessionFor(ctx context.Context, chatID string) *runtimeSession {
	p.mu.Lock()
	s := p.sessionByChatID[chatID]
	p.mu.Unlock()
	if s != nil {
		return s
	}

	dbSession, err := p.sessions.GetOrCreate(ctx, chatID)
	if err != nil || dbSession == nil {
		return nil
	}
	tracked, _ := p.sessions.ListTracked(ctx, chatID)
	trackedMap := make(map[string]entity.TrackedItem, len(tracked))
	for _, item := range tracked {
		trackedMap[item.ID] = item
	}

	s = &runtimeSession{
		ChatID:            chatID,
		Modes:             normalizeModes(dbSession.Modes),
		MinNetSpread:      ifZero(dbSession.MinNetSpread, 0.5),
		MinVolume:         maxFloat(0, dbSession.MinVolume),
		Page:              maxInt(0, dbSession.Page),
		UIScreen:          defaultString(dbSession.UIScreen, "main"),
		AlertEnabled:      dbSession.AlertEnabled,
		AlertThreshold:    ifZero(dbSession.AlertThreshold, 1),
		AlertSpikeMinVol:  maxFloat(0, dbSession.AlertSpikeMinVol),
		AlertSpikeModes:   normalizeAlertSpikeModes(dbSession.AlertSpikeModes),
		Tracked:           trackedMap,
		MenuMessageID:     dbSession.MenuMessageID,
		LastRenderedState: dbSession.LastRenderedState,
	}

	p.mu.Lock()
	if existing := p.sessionByChatID[chatID]; existing != nil {
		p.mu.Unlock()
		return existing
	}
	p.sessionByChatID[chatID] = s
	p.mu.Unlock()

	p.syncAlertRule(chatID, s)
	return s
}

func (p *Poller) syncAlertRule(chatID string, s *runtimeSession) {
	if p.alerts == nil || s == nil {
		return
	}
	snap := p.sessionSnapshot(s)
	tracked := trackedSlice(snap.Tracked)
	p.alerts.SyncChatRule(chatID, snap.AlertEnabled, snap.AlertThreshold, snap.AlertSpikeMinVol, snap.AlertSpikeModes, tracked)
}

func (p *Poller) persistSession(ctx context.Context, s *runtimeSession) {
	if s == nil {
		return
	}
	snap := p.sessionSnapshot(s)
	db := &entity.Session{
		ChatID:            snap.ChatID,
		Modes:             normalizeModes(snap.Modes),
		MinNetSpread:      snap.MinNetSpread,
		MinVolume:         snap.MinVolume,
		Page:              snap.Page,
		UIScreen:          snap.UIScreen,
		AlertEnabled:      snap.AlertEnabled,
		AlertThreshold:    snap.AlertThreshold,
		AlertSpikeMinVol:  snap.AlertSpikeMinVol,
		AlertSpikeModes:   normalizeAlertSpikeModes(snap.AlertSpikeModes),
		MenuMessageID:     snap.MenuMessageID,
		LastRenderedState: snap.LastRenderedState,
	}
	_ = p.sessions.Save(ctx, db)
	_ = p.sessions.ClearTracked(ctx, snap.ChatID)
	for _, item := range trackedSlice(snap.Tracked) {
		_ = p.sessions.UpsertTracked(ctx, snap.ChatID, item)
	}
	p.syncAlertRule(snap.ChatID, s)
}

func (p *Poller) sessionSnapshot(s *runtimeSession) sessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	tracked := make(map[string]entity.TrackedItem, len(s.Tracked))
	for k, v := range s.Tracked {
		tracked[k] = v
	}
	var menuID *int64
	if s.MenuMessageID != nil {
		x := *s.MenuMessageID
		menuID = &x
	}
	return sessionSnapshot{
		ChatID:            s.ChatID,
		Modes:             append([]string(nil), s.Modes...),
		MinNetSpread:      s.MinNetSpread,
		MinVolume:         s.MinVolume,
		Page:              s.Page,
		UIScreen:          s.UIScreen,
		AlertEnabled:      s.AlertEnabled,
		AlertThreshold:    s.AlertThreshold,
		AlertSpikeMinVol:  s.AlertSpikeMinVol,
		AlertSpikeModes:   append([]string(nil), s.AlertSpikeModes...),
		Tracked:           tracked,
		MenuMessageID:     menuID,
		LastRenderedState: s.LastRenderedState,
	}
}

func (p *Poller) getOpportunitiesSnapshot() opportunityCache {
	snap := p.watcher.Snapshot()
	p.mu.Lock()
	defer p.mu.Unlock()
	if snap.UpdatedAt != p.oppCache.UpdatedAt {
		byID := make(map[string]entity.Opportunity, len(snap.Opportunities))
		for _, item := range snap.Opportunities {
			byID[item.ID] = item
		}
		p.oppCache = opportunityCache{UpdatedAt: snap.UpdatedAt, Rows: snap.Opportunities, ByID: byID}
	}
	return p.oppCache
}

func (p *Poller) buildRows(snap sessionSnapshot) rowsState {
	cache := p.getOpportunitiesSnapshot()
	all := make([]entity.Opportunity, 0)
	enabledModes := mapFromSlice(normalizeModes(snap.Modes))
	for _, row := range cache.Rows {
		if _, ok := enabledModes[row.Market]; !ok {
			continue
		}
		if row.Market == "spot-futures" {
			valid := (row.BuyMarket == "spot" && row.SellMarket == "futures") || (row.BuyMarket == "futures" && row.SellMarket == "spot")
			if !valid {
				continue
			}
		}
		if row.NetSpreadPercent < snap.MinNetSpread {
			continue
		}
		if row.Volume24h < snap.MinVolume {
			continue
		}
		all = append(all, row)
	}
	totalPages := maxInt(1, int(math.Ceil(float64(len(all))/float64(pageSize))))
	page := snap.Page
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * pageSize
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	rows := []entity.Opportunity{}
	if start < len(all) {
		rows = all[start:end]
	}
	return rowsState{Rows: rows, Total: len(all), TotalPages: totalPages, Page: page}
}

func (p *Poller) renderSignature(v view) string {
	raw, _ := json.Marshal(v.ReplyMarkup)
	return v.Text + "\n\n" + string(raw)
}

func (p *Poller) sendMenu(ctx context.Context, chatID string) {
	s := p.sessionFor(ctx, chatID)
	if s == nil {
		return
	}
	snap := p.sessionSnapshot(s)
	v := p.buildView(snap)
	resp := p.botAPI(ctx, "sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     v.Text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
		"reply_markup":             v.ReplyMarkup,
	})
	if !resp.OK {
		return
	}
	var r struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(resp.Result, &r) != nil || r.MessageID == 0 {
		return
	}
	s.mu.Lock()
	s.MenuMessageID = &r.MessageID
	s.LastRenderedState = p.renderSignature(v)
	s.mu.Unlock()
	p.persistSession(ctx, s)
}

func (p *Poller) editMenu(ctx context.Context, chatID string, messageID int64, prepared *view) {
	s := p.sessionFor(ctx, chatID)
	if s == nil {
		return
	}

	s.mu.Lock()
	if s.SyncInFlight {
		s.SyncQueued = true
		s.mu.Unlock()
		return
	}
	s.SyncInFlight = true
	s.mu.Unlock()

	var v view
	if prepared != nil {
		v = *prepared
	} else {
		v = p.buildView(p.sessionSnapshot(s))
	}

	resp := p.botAPI(ctx, "editMessageText", map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     v.Text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
		"reply_markup":             v.ReplyMarkup,
	})

	s.mu.Lock()
	if resp.OK {
		x := messageID
		s.MenuMessageID = &x
		s.LastRenderedState = p.renderSignature(v)
	} else if strings.Contains(strings.ToLower(resp.Description), "message to edit not found") {
		s.MenuMessageID = nil
		s.LastRenderedState = ""
	}
	s.SyncInFlight = false
	queued := s.SyncQueued
	s.SyncQueued = false
	s.mu.Unlock()

	p.persistSession(ctx, s)

	if queued {
		p.editMenu(ctx, chatID, messageID, nil)
	}
}

func (p *Poller) refreshOpenMenus(ctx context.Context) {
	p.mu.Lock()
	items := make([]*runtimeSession, 0, len(p.sessionByChatID))
	for _, s := range p.sessionByChatID {
		items = append(items, s)
	}
	p.mu.Unlock()

	for _, s := range items {
		snap := p.sessionSnapshot(s)
		if snap.MenuMessageID == nil {
			continue
		}
		v := p.buildView(snap)
		sign := p.renderSignature(v)
		if sign == snap.LastRenderedState {
			continue
		}
		p.editMenu(ctx, snap.ChatID, *snap.MenuMessageID, &v)
	}
}

func (p *Poller) setMainScreen(s *runtimeSession) {
	s.mu.Lock()
	s.UIScreen = "main"
	s.mu.Unlock()
}

func (p *Poller) addTrackedFromRow(chatID string, s *runtimeSession, indexOneBased string) {
	idx, err := strconv.Atoi(strings.TrimSpace(indexOneBased))
	if err != nil || idx <= 0 {
		return
	}
	snap := p.sessionSnapshot(s)
	rows := p.buildRows(snap).Rows
	i := idx - 1
	if i < 0 || i >= len(rows) {
		return
	}
	row := rows[i]
	s.mu.Lock()
	if _, ok := s.Tracked[row.ID]; ok {
		delete(s.Tracked, row.ID)
	} else {
		s.Tracked[row.ID] = entity.TrackedItem{
			ID:           row.ID,
			Title:        fmt.Sprintf("%s %s(%s)→%s(%s)", row.Pair, row.BuyExchange, row.BuyMarket, row.SellExchange, row.SellMarket),
			Pair:         row.Pair,
			Market:       row.Market,
			BuyExchange:  row.BuyExchange,
			BuyMarket:    row.BuyMarket,
			SellExchange: row.SellExchange,
			SellMarket:   row.SellMarket,
			CreatedAt:    time.Now().UnixMilli(),
		}
	}
	s.mu.Unlock()
	p.syncAlertRule(chatID, s)
}

func (p *Poller) removeTrackedByIndex(chatID string, s *runtimeSession, indexOneBased string) {
	idx, err := strconv.Atoi(strings.TrimSpace(indexOneBased))
	if err != nil || idx <= 0 {
		return
	}
	s.mu.Lock()
	items := trackedSlice(s.Tracked)
	i := idx - 1
	if i >= 0 && i < len(items) {
		delete(s.Tracked, items[i].ID)
	}
	s.mu.Unlock()
	p.syncAlertRule(chatID, s)
}

func (p *Poller) applyCallback(chatID string, s *runtimeSession, data string) {
	if data == "rf" {
		return
	}
	s.mu.Lock()
	switch data {
	case "ui:main":
		s.UIScreen = "main"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:tracked":
		s.UIScreen = "tracked"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:filters":
		s.UIScreen = "filters"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:alerts":
		s.UIScreen = "alerts"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:filters:mode":
		s.UIScreen = "filters-mode"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:filters:net":
		s.UIScreen = "filters-net"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:filters:vol":
		s.UIScreen = "filters-vol"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:alerts:target":
		s.UIScreen = "alerts-target"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:alerts:spike-vol":
		s.UIScreen = "alerts-spike-vol"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	case "ui:alerts:spike-mode":
		s.UIScreen = "alerts-spike-mode"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "pg:") {
		delta, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(data, "pg:")))
		s.Page += delta
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "m:") {
		mode := strings.TrimPrefix(data, "m:")
		if containsString(modeOptions, mode) {
			selected := mapFromSlice(normalizeModes(s.Modes))
			if _, ok := selected[mode]; ok && len(selected) > 1 {
				delete(selected, mode)
			} else if !ok {
				selected[mode] = struct{}{}
			}
			s.Modes = intersectOrder(modeOptions, selected)
			s.Page = 0
			s.UIScreen = "filters-mode"
		}
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "sp:") {
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(data, "sp:")), 64)
		if err == nil && value > 0 {
			s.MinNetSpread = value
			s.Page = 0
			s.UIScreen = "main"
		}
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "vol:") {
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(data, "vol:")), 64)
		if err == nil && value >= 0 {
			s.MinVolume = value
			s.Page = 0
			s.UIScreen = "main"
		}
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if data == "al:toggle" {
		s.AlertEnabled = !s.AlertEnabled
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "al:target:") {
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(data, "al:target:")), 64)
		if err == nil && containsFloat(alertThresholds, value) {
			s.AlertThreshold = value
			s.UIScreen = "main"
		}
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "alv:") {
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(data, "alv:")), 64)
		if err == nil && containsFloat(alertSpikeVolumeOptions, value) {
			s.AlertSpikeMinVol = value
			s.UIScreen = "alerts-spike-vol"
		}
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}

	if strings.HasPrefix(data, "alm:") {
		mode := strings.TrimPrefix(data, "alm:")
		if containsString(modeOptions, mode) {
			selected := mapFromSlice(normalizeAlertSpikeModes(s.AlertSpikeModes))
			if _, ok := selected[mode]; ok && len(selected) > 1 {
				delete(selected, mode)
			} else if !ok {
				selected[mode] = struct{}{}
			}
			s.AlertSpikeModes = intersectOrder(modeOptions, selected)
			s.UIScreen = "alerts-spike-mode"
		}
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}
	if strings.HasPrefix(data, "trk:add:") {
		s.mu.Unlock()
		p.addTrackedFromRow(chatID, s, strings.TrimPrefix(data, "trk:add:"))
		p.setMainScreen(s)
		go p.persistSession(context.Background(), s)
		return
	}
	if strings.HasPrefix(data, "trk:rm:") {
		s.mu.Unlock()
		p.removeTrackedByIndex(chatID, s, strings.TrimPrefix(data, "trk:rm:"))
		s.mu.Lock()
		s.UIScreen = "tracked"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}
	if data == "trk:clear" {
		for k := range s.Tracked {
			delete(s.Tracked, k)
		}
		s.UIScreen = "tracked"
		s.mu.Unlock()
		go p.persistSession(context.Background(), s)
		return
	}
	s.mu.Unlock()
}

func (p *Poller) botAPI(ctx context.Context, method string, payload any) telegramAPIResponse {
	if p.cfg.Telegram.BotToken == "" {
		return telegramAPIResponse{}
	}
	var body *bytes.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	url := "https://api.telegram.org/bot" + p.cfg.Telegram.BotToken + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return telegramAPIResponse{}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return telegramAPIResponse{}
	}
	defer resp.Body.Close()
	var out telegramAPIResponse
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return telegramAPIResponse{}
	}
	return out
}
