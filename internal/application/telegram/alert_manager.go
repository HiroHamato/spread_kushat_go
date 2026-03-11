package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"spread_kushat_pora_golang/internal/config"
	"spread_kushat_pora_golang/internal/domain/entity"
)

const (
	alertHysteresis             = 0.08
	alertCooldownMS       int64 = 2 * 60 * 1000
	autoSpreadHysteresis        = 0.2
	autoSpreadCooldownMS  int64 = 5 * 60 * 1000
	autoSpreadMaxPerCycle       = 3
)

var autoSpreadModes = []string{"spot-futures", "spot", "futures"}

type metricState struct {
	Current     float64
	Smoothed    float64
	LastAlertAt int64
}

type spreadState struct {
	Active      bool
	LastAlertAt int64
}

type chatRule struct {
	ChatID         string
	Enabled        bool
	Threshold      float64
	SpikeMinVolume float64
	SpikeModes     []string
	Tracked        []entity.TrackedItem
}

type AlertManager struct {
	cfg  config.Config
	http *http.Client

	mu                   sync.Mutex
	chatRules            map[string]chatRule
	metricStateByKey     map[string]metricState
	autoSpreadStateByKey map[string]spreadState
	processing           bool
	pending              *pendingPayload
}

type pendingPayload struct {
	Opportunities []entity.Opportunity
	Now           int64
}

func NewAlertManager(cfg config.Config) *AlertManager {
	return &AlertManager{
		cfg:                  cfg,
		http:                 &http.Client{Timeout: 12 * time.Second},
		chatRules:            make(map[string]chatRule),
		metricStateByKey:     make(map[string]metricState),
		autoSpreadStateByKey: make(map[string]spreadState),
	}
}

func (a *AlertManager) SyncChatRule(chatID string, enabled bool, threshold float64, spikeMinVolume float64, spikeModes []string, tracked []entity.TrackedItem) {
	id := strings.TrimSpace(chatID)
	if id == "" {
		return
	}
	trackedIDs := make(map[string]struct{}, len(tracked))
	for _, item := range tracked {
		trackedIDs[item.ID] = struct{}{}
	}

	a.mu.Lock()
	a.chatRules[id] = chatRule{
		ChatID:         id,
		Enabled:        enabled,
		Threshold:      numOr(threshold, 0.5),
		SpikeMinVolume: maxFloat(0, spikeMinVolume),
		SpikeModes:     a.normalizeAutoSpreadModes(spikeModes),
		Tracked:        append([]entity.TrackedItem(nil), tracked...),
	}
	for key := range a.metricStateByKey {
		if !strings.HasPrefix(key, id+":") {
			continue
		}
		opID := strings.TrimPrefix(key, id+":")
		if _, ok := trackedIDs[opID]; !ok {
			delete(a.metricStateByKey, key)
		}
	}
	a.mu.Unlock()
}

func (a *AlertManager) RemoveChat(chatID string) {
	id := strings.TrimSpace(chatID)
	if id == "" {
		return
	}
	a.mu.Lock()
	delete(a.chatRules, id)
	for key := range a.metricStateByKey {
		if strings.HasPrefix(key, id+":") {
			delete(a.metricStateByKey, key)
		}
	}
	for key := range a.autoSpreadStateByKey {
		if strings.HasPrefix(key, id+":") {
			delete(a.autoSpreadStateByKey, key)
		}
	}
	a.mu.Unlock()
}

func (a *AlertManager) HandleOpportunities(ctx context.Context, opportunities []entity.Opportunity, now int64) {
	if a.cfg.Telegram.BotToken == "" || len(opportunities) == 0 {
		return
	}
	a.mu.Lock()
	a.pending = &pendingPayload{Opportunities: append([]entity.Opportunity(nil), opportunities...), Now: now}
	if a.processing {
		a.mu.Unlock()
		return
	}
	a.processing = true
	a.mu.Unlock()

	for {
		a.mu.Lock()
		payload := a.pending
		a.pending = nil
		a.mu.Unlock()
		if payload == nil {
			break
		}
		a.processPayload(ctx, payload.Opportunities, payload.Now)
	}

	a.mu.Lock()
	a.processing = false
	a.mu.Unlock()
}

func (a *AlertManager) processPayload(ctx context.Context, opportunities []entity.Opportunity, now int64) {
	a.mu.Lock()
	rules := make([]chatRule, 0, len(a.chatRules))
	for _, r := range a.chatRules {
		rules = append(rules, r)
	}
	a.mu.Unlock()
	if len(rules) == 0 {
		return
	}

	byID := make(map[string]entity.Opportunity, len(opportunities))
	for _, op := range opportunities {
		byID[op.ID] = op
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		a.processAutoSpread(ctx, rule, opportunities, now)
		a.processTrackedConvergence(ctx, rule, byID, now)
	}
}

func (a *AlertManager) processAutoSpread(ctx context.Context, rule chatRule, opportunities []entity.Opportunity, now int64) {
	queued := 0
	modeSet := make(map[string]struct{})
	for _, mode := range a.normalizeAutoSpreadModes(rule.SpikeModes) {
		modeSet[mode] = struct{}{}
	}

	for _, op := range opportunities {
		if queued >= autoSpreadMaxPerCycle {
			break
		}
		if op.IsMock || op.Volume24h < maxFloat(0, rule.SpikeMinVolume) {
			continue
		}
		if _, ok := modeSet[strings.ToLower(op.Market)]; !ok {
			continue
		}
		threshold := a.autoSpreadThresholdPercent(op.Market)
		if threshold == 0 {
			continue
		}
		if !a.shouldTriggerAutoSpreadAlert(rule.ChatID, op.ID, op.SpreadPercent, threshold, now) {
			continue
		}
		queued++
		_ = a.sendTelegram(ctx, rule.ChatID, a.formatAutoSpreadAlert(op, threshold))
	}
}

func (a *AlertManager) processTrackedConvergence(ctx context.Context, rule chatRule, byID map[string]entity.Opportunity, now int64) {
	if len(rule.Tracked) == 0 {
		return
	}
	for _, tracked := range rule.Tracked {
		opID := strings.TrimSpace(tracked.ID)
		if opID == "" {
			continue
		}
		op, ok := byID[opID]
		if !ok || op.IsMock {
			continue
		}
		current := op.NetSpreadPercent
		stateKey := rule.ChatID + ":" + opID

		a.mu.Lock()
		prev, exists := a.metricStateByKey[stateKey]
		if !exists {
			a.metricStateByKey[stateKey] = metricState{Current: current, Smoothed: current, LastAlertAt: 0}
			a.mu.Unlock()
			continue
		}
		smoothed := (prev.Smoothed * 0.7) + (current * 0.3)
		crossed := prev.Smoothed > (rule.Threshold+alertHysteresis) && smoothed <= rule.Threshold && current < prev.Current
		canSend := crossed && (now-prev.LastAlertAt >= alertCooldownMS)
		prev.Current = current
		prev.Smoothed = smoothed
		if canSend {
			prev.LastAlertAt = now
		}
		a.metricStateByKey[stateKey] = prev
		a.mu.Unlock()
		if canSend {
			_ = a.sendTelegram(ctx, rule.ChatID, a.formatConvergenceAlert(op, rule.Threshold, tracked))
		}
	}
}

func (a *AlertManager) shouldTriggerAutoSpreadAlert(chatID, opID string, spread, threshold float64, now int64) bool {
	key := chatID + ":" + opID
	a.mu.Lock()
	defer a.mu.Unlock()

	prev := a.autoSpreadStateByKey[key]
	resetLevel := maxFloat(0, threshold-autoSpreadHysteresis)
	if spread < threshold {
		if spread <= resetLevel {
			prev.Active = false
		}
		a.autoSpreadStateByKey[key] = prev
		return false
	}
	if prev.Active {
		a.autoSpreadStateByKey[key] = prev
		return false
	}
	if now-prev.LastAlertAt < autoSpreadCooldownMS {
		a.autoSpreadStateByKey[key] = prev
		return false
	}
	prev.Active = true
	prev.LastAlertAt = now
	a.autoSpreadStateByKey[key] = prev
	return true
}

func (a *AlertManager) normalizeAutoSpreadModes(modes []string) []string {
	selected := make(map[string]struct{})
	for _, m := range modes {
		selected[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}
	out := make([]string, 0, len(autoSpreadModes))
	for _, m := range autoSpreadModes {
		if _, ok := selected[m]; ok {
			out = append(out, m)
		}
	}
	if len(out) > 0 {
		return out
	}
	return append([]string(nil), autoSpreadModes...)
}

func (a *AlertManager) autoSpreadThresholdPercent(market string) float64 {
	switch strings.ToLower(strings.TrimSpace(market)) {
	case "spot", "futures":
		return 3
	case "spot-futures":
		return 5
	default:
		return 0
	}
}

func (a *AlertManager) sendTelegram(ctx context.Context, chatID, text string) bool {
	if a.cfg.Telegram.BotToken == "" || strings.TrimSpace(chatID) == "" {
		return false
	}
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	body, _ := json.Marshal(payload)
	url := "https://api.telegram.org/bot" + a.cfg.Telegram.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (a *AlertManager) formatAutoSpreadAlert(op entity.Opportunity, threshold float64) string {
	buyLink := exchangeLabelWithLink(op.BuyExchange, op.BuyMarket, op.Pair, op.BuyTradeURL)
	sellLink := exchangeLabelWithLink(op.SellExchange, op.SellMarket, op.Pair, op.SellTradeURL)
	buyFunding := formatFundingTag(op.BuyMarket, op.BuyFundingRate, op.BuyFundingNextTs)
	sellFunding := formatFundingTag(op.SellMarket, op.SellFundingRate, op.SellFundingNextTs)

	return strings.Join([]string{
		"<b>Новый высокий спред</b>",
		"<b>" + escapeHTML(op.Pair) + "</b> [" + escapeHTML(marketLabel(op.Market)) + "]",
		fmt.Sprintf("Порог: <b>%.2f%%</b> | Raw: <b>%.2f%%</b> | Net: <b>%.2f%%</b>", threshold, op.SpreadPercent, op.NetSpreadPercent),
		"Buy: " + buyLink + " (" + escapeHTML(buyFunding) + ") @ <code>" + strconv.FormatFloat(op.BuyPrice, 'f', 6, 64) + "</code>",
		"Sell: " + sellLink + " (" + escapeHTML(sellFunding) + ") @ <code>" + strconv.FormatFloat(op.SellPrice, 'f', 6, 64) + "</code>",
		"Vol: " + formatVolume(op.Volume24h),
	}, "\n")
}

func (a *AlertManager) formatConvergenceAlert(op entity.Opportunity, threshold float64, tracked entity.TrackedItem) string {
	buyLink := exchangeLabelWithLink(op.BuyExchange, op.BuyMarket, op.Pair, op.BuyTradeURL)
	sellLink := exchangeLabelWithLink(op.SellExchange, op.SellMarket, op.Pair, op.SellTradeURL)
	buyFunding := formatFundingTag(op.BuyMarket, op.BuyFundingRate, op.BuyFundingNextTs)
	sellFunding := formatFundingTag(op.SellMarket, op.SellFundingRate, op.SellFundingNextTs)
	title := op.Pair
	if strings.TrimSpace(tracked.Title) != "" {
		title = tracked.Title
	}

	return strings.Join([]string{
		"<b>Схождение спреда</b>",
		"<b>" + escapeHTML(title) + "</b> [" + escapeHTML(op.Market) + "]",
		fmt.Sprintf("Цель: <b>%.2f%%</b> | Net: <b>%.2f%%</b> | Raw: <b>%.2f%%</b>", threshold, op.NetSpreadPercent, op.SpreadPercent),
		"Buy: " + buyLink + " (" + escapeHTML(buyFunding) + ") @ <code>" + strconv.FormatFloat(op.BuyPrice, 'f', 6, 64) + "</code>",
		"Sell: " + sellLink + " (" + escapeHTML(sellFunding) + ") @ <code>" + strconv.FormatFloat(op.SellPrice, 'f', 6, 64) + "</code>",
		"Vol: " + formatVolume(op.Volume24h),
	}, "\n")
}

func numOr(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
