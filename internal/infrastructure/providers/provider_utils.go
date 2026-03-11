package providers

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

func isActive(set map[string]struct{}, symbol string) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[symbol]
	return ok
}

func normalizeKucoinFundingNextTS(v any) *int64 {
	ts := toInt64(v)
	if ts <= 0 {
		return nil
	}
	if ts < 100000000000 {
		n := time.Now().UnixMilli() + ts
		return &n
	}
	return &ts
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.TrimSpace(strings.ToUpper(v))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func containsString(in []string, x string) bool {
	for _, v := range in {
		if v == x {
			return true
		}
	}
	return false
}

func sanitizeSymbol(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0
		}
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		n, _ := t.Float64()
		return n
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return n
	default:
		return 0
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		if n != 0 {
			return n
		}
		f, _ := t.Float64()
		return int64(f)
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return n
		}
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr == nil {
			return int64(f)
		}
		return 0
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "1" || s == "true" || s == "yes" || s == "on"
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return false
	}
}

func ptrFloat(v float64) *float64 {
	if v == 0 {
		return nil
	}
	x := v
	return &x
}

func ptrInt64(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	x := v
	return &x
}

func ptrString(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	x := v
	return &x
}

func coalesce(items ...any) any {
	for _, item := range items {
		if item == nil {
			continue
		}
		switch t := item.(type) {
		case string:
			if strings.TrimSpace(t) == "" {
				continue
			}
		}
		return item
	}
	return nil
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func ternaryFloat(cond bool, a, b float64) float64 {
	if cond {
		return a
	}
	return b
}

func jitter(value, pct float64) float64 {
	delta := value * pct
	return value + ((rand.Float64()*2 - 1) * delta)
}

func basePrice(symbol string) float64 {
	switch {
	case strings.HasPrefix(symbol, "BTC"):
		return 96000
	case strings.HasPrefix(symbol, "ETH"):
		return 3200
	case strings.HasPrefix(symbol, "SOL"):
		return 170
	case strings.HasPrefix(symbol, "XRP"):
		return 2.5
	case strings.HasPrefix(symbol, "ADA"):
		return 0.9
	default:
		return 0.28
	}
}

func exchangeBias(exchange string) float64 {
	switch exchange {
	case "Binance":
		return 1
	case "OKX":
		return 0.9996
	case "Bybit":
		return 1.0005
	case "MEXC":
		return 0.9988
	case "BingX":
		return 1.001
	case "KuCoin":
		return 1.0002
	case "Gate.io":
		return 1.0003
	case "Hyperliquid":
		return 1.0009
	case "Dexscreener":
		return 1.0012
	default:
		return 1
	}
}
