package telegram

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func trimZeros(text string) string {
	for strings.Contains(text, ".") && strings.HasSuffix(text, "0") {
		text = strings.TrimSuffix(text, "0")
	}
	text = strings.TrimSuffix(text, ".")
	return text
}

func formatVolume(volume float64) string {
	n := volume
	if n < 0 {
		n = 0
	}
	if n >= 1_000_000_000 {
		return trimZeros(fmt.Sprintf("%.1f", n/1_000_000_000)) + "kkk"
	}
	if n >= 1_000_000 {
		return trimZeros(fmt.Sprintf("%.1f", n/1_000_000)) + "kk"
	}
	if n >= 1_000 {
		return trimZeros(fmt.Sprintf("%.1f", n/1_000)) + "k"
	}
	return fmt.Sprintf("%.0f", math.Round(n))
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

func splitSymbol(symbol string) (base, quote string) {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if strings.HasSuffix(s, "USDT") {
		return strings.TrimSuffix(s, "USDT"), "USDT"
	}
	if strings.HasSuffix(s, "USDC") {
		return strings.TrimSuffix(s, "USDC"), "USDC"
	}
	return s, "USDT"
}

func exchangePairURL(exchange, market, symbol string) string {
	ex := strings.TrimSpace(exchange)
	m := strings.ToLower(strings.TrimSpace(market))
	if m == "" {
		m = "spot"
	}
	base, quote := splitSymbol(symbol)
	pairUnderscore := base + "_" + quote
	pairDashLower := strings.ToLower(base + "-" + quote)
	raw := base + quote

	switch ex {
	case "Binance":
		if m == "futures" {
			return "https://www.binance.com/en/futures/" + raw
		}
		return "https://www.binance.com/en/trade/" + pairUnderscore + "?type=spot"
	case "OKX":
		if m == "futures" {
			return "https://www.okx.com/trade-swap/" + pairDashLower + "-swap"
		}
		return "https://www.okx.com/trade-spot/" + pairDashLower
	case "Bybit":
		if m == "futures" {
			return "https://www.bybit.com/trade/usdt/" + raw
		}
		return "https://www.bybit.com/trade/spot/" + base + "/" + quote
	case "MEXC":
		if m == "futures" {
			return "https://futures.mexc.com/exchange/" + pairUnderscore
		}
		return "https://www.mexc.com/exchange/" + pairUnderscore
	case "BingX":
		if m == "futures" {
			return "https://bingx.com/en-us/futures/forward/" + raw
		}
		return "https://bingx.com/en-us/spot/" + raw
	case "KuCoin":
		if m == "futures" {
			return "https://www.kucoin.com/futures/trade/" + base + quote + "M"
		}
		return "https://www.kucoin.com/trade/" + base + "-" + quote
	case "Gate.io":
		if m == "futures" {
			return "https://www.gate.io/futures_trade/USDT/" + pairUnderscore
		}
		return "https://www.gate.io/trade/" + pairUnderscore
	case "Hyperliquid":
		return "https://app.hyperliquid.xyz/trade/" + base
	case "Dexscreener":
		return "https://dexscreener.com/search?q=" + raw
	default:
		return ""
	}
}

func exchangeLabelWithLink(exchange, market, symbol string, directURL *string) string {
	label := escapeHTML(exchange) + " (" + escapeHTML(strings.ToUpper(defaultString(market, "spot"))) + ")"
	url := ""
	if directURL != nil {
		url = strings.TrimSpace(*directURL)
	}
	if url == "" {
		url = exchangePairURL(exchange, market, symbol)
	}
	if url == "" {
		return label
	}
	return "<a href=\"" + escapeHTML(url) + "\">" + label + "</a>"
}

func formatFundingWait(nextTS *int64) string {
	if nextTS == nil || *nextTS <= 0 {
		return "n/a"
	}
	ts := *nextTS
	if ts < 1_000_000_000_000 {
		ts *= 1000
	}
	delta := ts - time.Now().UnixMilli()
	if delta < 0 {
		delta = 0
	}
	totalMin := delta / 60000
	h := totalMin / 60
	m := totalMin % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func formatFundingTag(market string, rate *float64, nextTS *int64) string {
	if strings.ToLower(strings.TrimSpace(market)) != "futures" || rate == nil {
		return "funding n/a"
	}
	n := *rate
	pct := math.Abs(n * 100)
	digits := 4
	if pct >= 1 {
		digits = 2
	} else if pct >= 0.1 {
		digits = 3
	}
	sign := ""
	if n > 0 {
		sign = "+"
	} else if n < 0 {
		sign = "-"
	}
	rateText := sign + fmt.Sprintf("%.*f%%", digits, math.Abs(n*100))
	waitText := formatFundingWait(nextTS)
	if waitText == "n/a" {
		return "funding " + rateText
	}
	return "funding " + rateText + " in " + waitText
}

func formatDateTime(ts *int64) string {
	if ts == nil || *ts <= 0 {
		return "n/a"
	}
	d := time.UnixMilli(*ts).UTC().Add(3 * time.Hour)
	return d.Format("02.01 15:04") + " MSK"
}

func formatDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	totalSec := ms / 1000
	days := totalSec / 86400
	hours := (totalSec % 86400) / 3600
	mins := (totalSec % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func marketLabel(market string) string {
	switch strings.ToLower(strings.TrimSpace(market)) {
	case "spot":
		return "SPOT-SPOT"
	case "futures":
		return "FUTURES-FUTURES"
	case "spot-futures":
		return "SPOT-FUTURES"
	default:
		return strings.ToUpper(defaultString(market, "n/a"))
	}
}

func defaultString(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}
