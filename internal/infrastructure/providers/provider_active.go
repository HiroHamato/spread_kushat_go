package providers

import (
	"context"
	"strings"
	"time"

	"spread_kushat_pora_golang/internal/shared"
)

func (p *Provider) getBinanceActiveSpot(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("Binance", "spot", 300000); set != nil {
		return set
	}
	var resp struct {
		Symbols []map[string]any `json:"symbols"`
	}
	if !p.fetchJSON(ctx, "GET", "https://api.binance.com/api/v3/exchangeInfo", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Symbols {
		if strings.ToUpper(toString(row["status"])) != "TRADING" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("Binance", "spot", set)
	return set
}

func (p *Provider) getBinanceActiveFutures(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("Binance", "futures", 300000); set != nil {
		return set
	}
	var resp struct {
		Symbols []map[string]any `json:"symbols"`
	}
	if !p.fetchJSON(ctx, "GET", "https://fapi.binance.com/fapi/v1/exchangeInfo", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Symbols {
		if strings.ToUpper(toString(row["status"])) != "TRADING" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("Binance", "futures", set)
	return set
}

func (p *Provider) getOKXActive(ctx context.Context, market string) map[string]struct{} {
	if set := p.getActiveCache("OKX", market, 300000); set != nil {
		return set
	}
	instType := "SPOT"
	if market == "futures" {
		instType = "SWAP"
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://www.okx.com/api/v5/public/instruments?instType="+instType, nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Data {
		state := strings.ToLower(toString(row["state"]))
		if state != "live" && state != "trading" {
			continue
		}
		symbol := shared.NormalizeOKXSymbol(toString(row["instId"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("OKX", market, set)
	return set
}

func (p *Provider) getBybitActive(ctx context.Context, market string) map[string]struct{} {
	if set := p.getActiveCache("Bybit", market, 300000); set != nil {
		return set
	}
	category := "spot"
	if market == "futures" {
		category = "linear"
	}
	var resp struct {
		Result struct {
			List []map[string]any `json:"list"`
		} `json:"result"`
	}
	if !p.fetchJSON(ctx, "GET", "https://api.bybit.com/v5/market/instruments-info?category="+category+"&limit=1000", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Result.List {
		status := strings.ToLower(toString(coalesce(row["status"], row["symbolStatus"])))
		if status != "trading" && status != "tradable" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("Bybit", market, set)
	return set
}

func (p *Provider) getMEXCActiveSpot(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("MEXC", "spot", 300000); set != nil {
		return set
	}
	var resp struct {
		Symbols []map[string]any `json:"symbols"`
	}
	if !p.fetchJSON(ctx, "GET", "https://api.mexc.com/api/v3/exchangeInfo", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Symbols {
		status := strings.ToLower(toString(row["status"]))
		if status != "enabled" && status != "1" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("MEXC", "spot", set)
	return set
}

func (p *Provider) getMEXCActiveFutures(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("MEXC", "futures", 300000); set != nil {
		return set
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://contract.mexc.com/api/v1/contract/detail", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Data {
		state := strings.ToLower(toString(row["state"]))
		if state != "0" && state != "enabled" && state != "open" {
			continue
		}
		symbol := strings.TrimSpace(toString(row["symbol"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("MEXC", "futures", set)
	return set
}

func (p *Provider) getKucoinActiveSpot(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("KuCoin", "spot", 300000); set != nil {
		return set
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://api.kucoin.com/api/v2/symbols", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Data {
		if !toBool(coalesce(row["enableTrading"], row["isTrading"])) {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(coalesce(row["symbol"], row["name"])))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("KuCoin", "spot", set)
	return set
}

func (p *Provider) getGateActiveSpot(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("Gate.io", "spot", 300000); set != nil {
		return set
	}
	var arr []map[string]any
	if !p.fetchJSON(ctx, "GET", "https://api.gateio.ws/api/v4/spot/currency_pairs", nil, &arr) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range arr {
		status := strings.ToLower(toString(coalesce(row["trade_status"], row["status"])))
		if status != "tradable" && status != "trading" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["id"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("Gate.io", "spot", set)
	return set
}

func (p *Provider) getGateActiveFutures(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("Gate.io", "futures", 300000); set != nil {
		return set
	}
	var arr []map[string]any
	if !p.fetchJSON(ctx, "GET", "https://api.gateio.ws/api/v4/futures/usdt/contracts", nil, &arr) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range arr {
		if toBool(row["in_delisting"]) {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["name"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("Gate.io", "futures", set)
	return set
}

func (p *Provider) getActiveCache(exchange, market string, ttlMS int64) map[string]struct{} {
	p.mu.RLock()
	var item cacheSet
	var ok bool
	if market == "spot" {
		item, ok = p.activeSpot[exchange]
	} else {
		item, ok = p.activeFutures[exchange]
	}
	p.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Now().UnixMilli()-item.ts > ttlMS {
		return nil
	}
	return item.data
}

func (p *Provider) setActiveCache(exchange, market string, data map[string]struct{}) {
	if len(data) == 0 {
		return
	}
	p.mu.Lock()
	item := cacheSet{ts: time.Now().UnixMilli(), data: data}
	if market == "spot" {
		p.activeSpot[exchange] = item
	} else {
		p.activeFutures[exchange] = item
	}
	p.mu.Unlock()
}
