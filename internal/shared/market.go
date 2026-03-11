package shared

import (
	"regexp"
	"strings"
)

var nonAlphaNum = regexp.MustCompile(`[^A-Z0-9]`)

func NormalizeSymbol(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	s := strings.ToUpper(raw)
	s = nonAlphaNum.ReplaceAllString(s, "")
	switch {
	case strings.HasSuffix(s, "USDT"):
		return s
	case strings.HasSuffix(s, "USDC"):
		return s
	case strings.HasSuffix(s, "USD"):
		return strings.TrimSuffix(s, "USD") + "USDT"
	default:
		return ""
	}
}

func SymbolBase(symbol string) string {
	s := NormalizeSymbol(symbol)
	switch {
	case strings.HasSuffix(s, "USDT"):
		return strings.TrimSuffix(s, "USDT")
	case strings.HasSuffix(s, "USDC"):
		return strings.TrimSuffix(s, "USDC")
	default:
		return ""
	}
}

func NormalizeOKXSymbol(instID string) string {
	if strings.TrimSpace(instID) == "" {
		return ""
	}
	parts := strings.Split(strings.ToUpper(instID), "-")
	if len(parts) < 2 {
		return NormalizeSymbol(instID)
	}
	quote := parts[1]
	if quote != "USDT" && quote != "USDC" {
		return ""
	}
	return NormalizeSymbol(parts[0] + quote)
}

func NormalizeKucoinFuturesSymbol(contractSymbol, baseCurrency, quoteCurrency string) string {
	base := nonAlphaNum.ReplaceAllString(strings.ToUpper(baseCurrency), "")
	quote := nonAlphaNum.ReplaceAllString(strings.ToUpper(quoteCurrency), "")
	if base != "" && (quote == "USDT" || quote == "USDC") {
		return NormalizeSymbol(base + quote)
	}

	raw := nonAlphaNum.ReplaceAllString(strings.ToUpper(contractSymbol), "")
	if strings.HasSuffix(raw, "USDTM") {
		return NormalizeSymbol(strings.TrimSuffix(raw, "M"))
	}
	if strings.HasSuffix(raw, "USDCM") {
		return NormalizeSymbol(strings.TrimSuffix(raw, "M"))
	}
	return NormalizeSymbol(raw)
}
