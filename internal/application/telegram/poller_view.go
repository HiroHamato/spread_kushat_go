package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"spread_kushat_pora_golang/internal/domain/entity"
)

func (p *Poller) buildMainText(snap sessionSnapshot, rs rowsState) string {
	head := []string{
		"Spread Scanner Bot 🚨",
		"Mode: " + modeLabel(snap.Modes) + " | MinNet: " + trimZeros(fmt.Sprintf("%.2f", snap.MinNetSpread)) + "% | MinVol: " + formatVolume(snap.MinVolume),
		fmt.Sprintf("Tracked: %d | Alerts: %s | Target: %s%%", len(snap.Tracked), ternaryBool(snap.AlertEnabled, "ON", "OFF"), trimZeros(fmt.Sprintf("%.2f", snap.AlertThreshold))),
		fmt.Sprintf("Page %d/%d | Total %d", rs.Page+1, rs.TotalPages, rs.Total),
		"",
	}
	if len(rs.Rows) == 0 {
		return strings.Join(append(head, "No opportunities for current filters."), "\n")
	}
	blocks := make([]string, 0, len(rs.Rows))
	now := time.Now().UnixMilli()
	for i, row := range rs.Rows {
		tracked := "▫️"
		if _, ok := snap.Tracked[row.ID]; ok {
			tracked = "✅"
		}
		trendLine := p.formatTrendLine(row, now)
		blocks = append(blocks,
			fmt.Sprintf("%d. %s %s [%s]\nBuy: %s (%s) @ %.6f\nSell: %s (%s) @ %.6f\nSpread: %.2f%% | Net: %.2f%% | Vol: %s\n%s",
				i+1,
				tracked,
				escapeHTML(row.Pair),
				escapeHTML(row.Market),
				exchangeLabelWithLink(row.BuyExchange, defaultString(row.BuyMarket, "spot"), row.Pair, row.BuyTradeURL),
				formatFundingTag(row.BuyMarket, row.BuyFundingRate, row.BuyFundingNextTs),
				row.BuyPrice,
				exchangeLabelWithLink(row.SellExchange, defaultString(row.SellMarket, "spot"), row.Pair, row.SellTradeURL),
				formatFundingTag(row.SellMarket, row.SellFundingRate, row.SellFundingNextTs),
				row.SellPrice,
				row.SpreadPercent,
				row.NetSpreadPercent,
				formatVolume(row.Volume24h),
				trendLine,
			),
		)
	}
	return strings.Join(append(head, blocks...), "\n\n")
}

func (p *Poller) buildTrackedText(snap sessionSnapshot) string {
	tracked := trackedSlice(snap.Tracked)
	cache := p.getOpportunitiesSnapshot()
	head := []string{
		"Отслеживаемые пары 📌",
		fmt.Sprintf("Всего: %d", len(tracked)),
		fmt.Sprintf("Alerts: %s | Target: %s%%", ternaryBool(snap.AlertEnabled, "ON", "OFF"), trimZeros(fmt.Sprintf("%.2f", snap.AlertThreshold))),
		"",
	}
	if len(tracked) == 0 {
		return strings.Join(append(head, "Список пуст."), "\n")
	}
	lines := make([]string, 0, len(tracked))
	now := time.Now().UnixMilli()
	for idx, item := range tracked {
		op, ok := cache.ByID[item.ID]
		if !ok {
			lines = append(lines, fmt.Sprintf("%d. %s\nСейчас не найдено в текущем срезе.", idx+1, escapeHTML(item.Title)))
			continue
		}
		lines = append(lines,
			fmt.Sprintf("%d. %s\nSpread: %.2f%% | Net: %.2f%% | Vol: %s\n%s",
				idx+1,
				escapeHTML(item.Title),
				op.SpreadPercent,
				op.NetSpreadPercent,
				formatVolume(op.Volume24h),
				p.formatTrendLine(op, now),
			),
		)
	}
	return strings.Join(append(head, lines...), "\n\n")
}

func (p *Poller) formatTrendLine(op entity.Opportunity, now int64) string {
	trend := p.watcher.SpreadTrendForOpportunity(op, now)
	switch trend.State {
	case "converging":
		start := 0.0
		if trend.StartPercent != nil {
			start = *trend.StartPercent
		}
		return fmt.Sprintf("Тренд ⬆️: сводится с %s от %.2f%% (%s)", formatDateTime(trend.SinceTS), start, formatDuration(trend.DurationMS))
	case "diverging":
		start := 0.0
		if trend.StartPercent != nil {
			start = *trend.StartPercent
		}
		return fmt.Sprintf("Тренд ⬇️: расходится с %s от %.2f%% (%s)", formatDateTime(trend.SinceTS), start, formatDuration(trend.DurationMS))
	case "stable":
		return "Тренд: без выраженного движения"
	default:
		return "Тренд: недостаточно истории"
	}
}

func (p *Poller) buildView(snap sessionSnapshot) view {
	screen := defaultString(snap.UIScreen, "main")
	if screen == "tracked" {
		return view{Text: p.buildTrackedText(snap), ReplyMarkup: p.buildTrackedKeyboard(snap)}
	}
	rs := p.buildRows(snap)
	text := p.buildMainText(snap, rs)
	switch screen {
	case "filters":
		return view{Text: text, ReplyMarkup: p.buildFiltersKeyboard(snap)}
	case "filters-mode":
		return view{Text: text, ReplyMarkup: p.buildFiltersModeKeyboard(snap)}
	case "filters-net":
		return view{Text: text, ReplyMarkup: p.buildFiltersNetKeyboard(snap)}
	case "filters-vol":
		return view{Text: text, ReplyMarkup: p.buildFiltersVolumeKeyboard(snap)}
	case "alerts":
		return view{Text: text, ReplyMarkup: p.buildAlertsKeyboard(snap)}
	case "alerts-target":
		return view{Text: text, ReplyMarkup: p.buildAlertsTargetKeyboard(snap)}
	case "alerts-spike-vol":
		return view{Text: text, ReplyMarkup: p.buildAlertsSpikeVolumeKeyboard(snap)}
	case "alerts-spike-mode":
		return view{Text: text, ReplyMarkup: p.buildAlertsSpikeModesKeyboard(snap)}
	default:
		return view{Text: text, ReplyMarkup: p.buildMainKeyboard(snap, rs)}
	}
}

func (p *Poller) buildMainKeyboard(snap sessionSnapshot, rs rowsState) map[string]any {
	numberButtons := make([]map[string]string, 0, len(rs.Rows))
	for i, row := range rs.Rows {
		text := strconv.Itoa(i + 1)
		if _, ok := snap.Tracked[row.ID]; ok {
			text = text + " ✅"
		}
		numberButtons = append(numberButtons, map[string]string{"text": text, "callback_data": fmt.Sprintf("trk:add:%d", i+1)})
	}
	keyboard := []any{
		[]map[string]string{{"text": "⬅ Prev", "callback_data": "pg:-1"}, {"text": "Next ➡", "callback_data": "pg:+1"}},
		[]map[string]string{{"text": "🔄 Refresh", "callback_data": "rf"}},
	}
	for _, chunk := range chunkButtons(numberButtons, 4) {
		keyboard = append(keyboard, chunk)
	}
	keyboard = append(keyboard, []map[string]string{{"text": fmt.Sprintf("📌 Отслеживаемые (%d)", len(snap.Tracked)), "callback_data": "ui:tracked"}})
	keyboard = append(keyboard, []map[string]string{{"text": "🧪 Filters", "callback_data": "ui:filters"}, {"text": "🚨 Alerts " + stateIcon(snap.AlertEnabled), "callback_data": "ui:alerts"}})
	return map[string]any{"inline_keyboard": keyboard}
}

func (p *Poller) buildFiltersKeyboard(snap sessionSnapshot) map[string]any {
	return map[string]any{"inline_keyboard": []any{
		[]map[string]string{{"text": "🧭 Mode (" + modeLabel(snap.Modes) + ")", "callback_data": "ui:filters:mode"}},
		[]map[string]string{{"text": "📈 Net % (" + trimZeros(fmt.Sprintf("%.2f", snap.MinNetSpread)) + ")", "callback_data": "ui:filters:net"}},
		[]map[string]string{{"text": "💧 Volume (" + formatVolume(snap.MinVolume) + ")", "callback_data": "ui:filters:vol"}},
		[]map[string]string{{"text": "⬅️ Back", "callback_data": "ui:main"}},
	}}
}

func (p *Poller) buildFiltersModeKeyboard(snap sessionSnapshot) map[string]any {
	modes := mapFromSlice(normalizeModes(snap.Modes))
	return map[string]any{"inline_keyboard": []any{
		[]map[string]string{{"text": selectedIcon(hasKey(modes, "spot-futures")) + " SPOT-FUTURE", "callback_data": "m:spot-futures"}},
		[]map[string]string{{"text": selectedIcon(hasKey(modes, "spot")) + " SPOT-SPOT", "callback_data": "m:spot"}},
		[]map[string]string{{"text": selectedIcon(hasKey(modes, "futures")) + " FUTURE-FUTURE", "callback_data": "m:futures"}},
		[]map[string]string{{"text": "⬅️ Back to Filters", "callback_data": "ui:filters"}},
	}}
}

func (p *Poller) buildFiltersNetKeyboard(snap sessionSnapshot) map[string]any {
	return map[string]any{"inline_keyboard": []any{
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinNetSpread, 0.5)) + " Net >=0.5%", "callback_data": "sp:0.5"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinNetSpread, 1)) + " Net >=1%", "callback_data": "sp:1"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinNetSpread, 2)) + " Net >=2%", "callback_data": "sp:2"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinNetSpread, 5)) + " Net >=5%", "callback_data": "sp:5"}},
		[]map[string]string{{"text": "⬅️ Back to Filters", "callback_data": "ui:filters"}},
	}}
}

func (p *Poller) buildFiltersVolumeKeyboard(snap sessionSnapshot) map[string]any {
	return map[string]any{"inline_keyboard": []any{
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinVolume, 0)) + " >=0", "callback_data": "vol:0"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinVolume, 100000)) + " >=100k", "callback_data": "vol:100000"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinVolume, 500000)) + " >=500k", "callback_data": "vol:500000"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinVolume, 1000000)) + " >=1kk", "callback_data": "vol:1000000"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinVolume, 50000000)) + " >=50kk", "callback_data": "vol:50000000"}},
		[]map[string]string{{"text": selectedIcon(floatEq(snap.MinVolume, 100000000)) + " >=100kk", "callback_data": "vol:100000000"}},
		[]map[string]string{{"text": "⬅️ Back to Filters", "callback_data": "ui:filters"}},
	}}
}

func (p *Poller) buildAlertsKeyboard(snap sessionSnapshot) map[string]any {
	return map[string]any{"inline_keyboard": []any{
		[]map[string]string{{"text": stateIcon(snap.AlertEnabled) + " Alerts", "callback_data": "al:toggle"}},
		[]map[string]string{{"text": "🎯 Target (" + trimZeros(fmt.Sprintf("%.2f", snap.AlertThreshold)) + "%)", "callback_data": "ui:alerts:target"}},
		[]map[string]string{{"text": "⚡ Big Spread Vol (" + formatVolume(snap.AlertSpikeMinVol) + ")", "callback_data": "ui:alerts:spike-vol"}},
		[]map[string]string{{"text": "⚡ Big Spread Modes (" + alertSpikeModesLabel(snap.AlertSpikeModes) + ")", "callback_data": "ui:alerts:spike-mode"}},
		[]map[string]string{{"text": "⬅️ Back", "callback_data": "ui:main"}},
	}}
}

func (p *Poller) buildAlertsTargetKeyboard(snap sessionSnapshot) map[string]any {
	rows := make([]any, 0, len(alertThresholds)+1)
	for _, v := range alertThresholds {
		rows = append(rows, []map[string]string{{"text": selectedIcon(floatEq(snap.AlertThreshold, v)) + " " + trimZeros(fmt.Sprintf("%.2f", v)) + "%", "callback_data": "al:target:" + trimZeros(fmt.Sprintf("%.2f", v))}})
	}
	rows = append(rows, []map[string]string{{"text": "⬅️ Back to Alerts", "callback_data": "ui:alerts"}})
	return map[string]any{"inline_keyboard": rows}
}

func (p *Poller) buildAlertsSpikeVolumeKeyboard(snap sessionSnapshot) map[string]any {
	rows := make([]any, 0, len(alertSpikeVolumeOptions)+1)
	for _, v := range alertSpikeVolumeOptions {
		rows = append(rows, []map[string]string{{"text": selectedIcon(floatEq(snap.AlertSpikeMinVol, v)) + " >=" + formatVolume(v), "callback_data": "alv:" + strconv.FormatInt(int64(v), 10)}})
	}
	rows = append(rows, []map[string]string{{"text": "⬅️ Back to Alerts", "callback_data": "ui:alerts"}})
	return map[string]any{"inline_keyboard": rows}
}

func (p *Poller) buildAlertsSpikeModesKeyboard(snap sessionSnapshot) map[string]any {
	modes := mapFromSlice(normalizeAlertSpikeModes(snap.AlertSpikeModes))
	return map[string]any{"inline_keyboard": []any{
		[]map[string]string{{"text": selectedIcon(hasKey(modes, "spot-futures")) + " SPOT-FUTURE", "callback_data": "alm:spot-futures"}},
		[]map[string]string{{"text": selectedIcon(hasKey(modes, "spot")) + " SPOT-SPOT", "callback_data": "alm:spot"}},
		[]map[string]string{{"text": selectedIcon(hasKey(modes, "futures")) + " FUTURE-FUTURE", "callback_data": "alm:futures"}},
		[]map[string]string{{"text": "⬅️ Back to Alerts", "callback_data": "ui:alerts"}},
	}}
}

func (p *Poller) buildTrackedKeyboard(snap sessionSnapshot) map[string]any {
	tracked := trackedSlice(snap.Tracked)
	removeButtons := make([]map[string]string, 0, len(tracked))
	for idx := range tracked {
		removeButtons = append(removeButtons, map[string]string{"text": fmt.Sprintf("❌ %d", idx+1), "callback_data": fmt.Sprintf("trk:rm:%d", idx+1)})
	}
	rows := make([]any, 0)
	for _, c := range chunkButtons(removeButtons, 4) {
		rows = append(rows, c)
	}
	if len(removeButtons) > 0 {
		rows = append(rows, []map[string]string{{"text": "🧹 Очистить все", "callback_data": "trk:clear"}})
	}
	rows = append(rows, []map[string]string{{"text": "⬅️ Back", "callback_data": "ui:main"}})
	return map[string]any{"inline_keyboard": rows}
}
