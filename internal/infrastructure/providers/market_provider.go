package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"spread_kushat_pora_golang/internal/config"
	"spread_kushat_pora_golang/internal/domain/entity"
	"spread_kushat_pora_golang/internal/shared"
)

var networks = []entity.Network{
	{Name: "ERC20", Deposit: true, Withdraw: true},
	{Name: "TRC20", Deposit: true, Withdraw: true},
	{Name: "Arbitrum", Deposit: true, Withdraw: true},
	{Name: "BSC", Deposit: true, Withdraw: true},
	{Name: "Polygon", Deposit: true, Withdraw: true},
}

var coreSymbols = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "XRPUSDT", "ADAUSDT", "DOGEUSDT"}

type cacheSet struct {
	ts   int64
	data map[string]struct{}
}

type Provider struct {
	cfg        config.Config
	httpClient *http.Client

	mu                 sync.RWMutex
	initialized        bool
	mode               string
	availableExchanges map[string]struct{}

	activeSpot    map[string]cacheSet
	activeFutures map[string]cacheSet

	okxFundingMu sync.Mutex
	okxFunding   map[string]fundingValue
	mexcFundMu   sync.Mutex
	mexcFunding  map[string]fundingValue
}

type fundingValue struct {
	Rate   *float64
	NextTS *int64
	TS     int64
}

type fetchCall struct {
	exchange string
	market   string
}

func NewProvider(cfg config.Config) *Provider {
	return &Provider{
		cfg:                cfg,
		httpClient:         &http.Client{Timeout: time.Duration(cfg.ProviderTimeoutMS) * time.Millisecond},
		availableExchanges: make(map[string]struct{}),
		activeSpot:         make(map[string]cacheSet),
		activeFutures:      make(map[string]cacheSet),
		okxFunding:         make(map[string]fundingValue),
		mexcFunding:        make(map[string]fundingValue),
	}
}

func (p *Provider) GetQuotes(ctx context.Context, mode string) ([]entity.Quote, error) {
	markets := modeToMarkets(mode)
	if !p.initialized || p.mode != mode {
		rows, reachable := p.collectQuotes(ctx, p.cfg.Exchanges, markets)
		p.mu.Lock()
		p.initialized = true
		p.mode = mode
		p.availableExchanges = reachable
		p.mu.Unlock()
		if len(rows) > 0 {
			return rows, nil
		}
		if p.cfg.UseMockFallback {
			return p.mockFallback(mode), nil
		}
		return []entity.Quote{}, nil
	}

	p.mu.RLock()
	available := make([]string, 0, len(p.availableExchanges))
	for ex := range p.availableExchanges {
		available = append(available, ex)
	}
	p.mu.RUnlock()

	if len(available) == 0 {
		if p.cfg.UseMockFallback {
			return p.mockFallback(mode), nil
		}
		return []entity.Quote{}, nil
	}

	rows, _ := p.collectQuotes(ctx, available, markets)
	if len(rows) > 0 {
		return rows, nil
	}
	if p.cfg.UseMockFallback {
		return p.mockFallback(mode), nil
	}
	return []entity.Quote{}, nil
}

func (p *Provider) collectQuotes(ctx context.Context, exchanges, markets []string) ([]entity.Quote, map[string]struct{}) {
	hasDex := false
	base := make([]string, 0, len(exchanges))
	for _, ex := range exchanges {
		if ex == "Dexscreener" {
			hasDex = true
			continue
		}
		base = append(base, ex)
	}

	calls := make([]fetchCall, 0)
	for _, ex := range base {
		for _, market := range markets {
			calls = append(calls, fetchCall{exchange: ex, market: market})
		}
	}

	rows := make([]entity.Quote, 0, 2048)
	reachable := make(map[string]struct{})
	var rowsMu sync.Mutex
	var wg sync.WaitGroup

	for _, call := range calls {
		call := call
		wg.Add(1)
		go func() {
			defer wg.Done()
			exchangeCtx, cancel := context.WithTimeout(ctx, time.Duration(p.cfg.ProviderExchangeTimeout)*time.Millisecond)
			defer cancel()
			chunk := p.fetchExchange(exchangeCtx, call.exchange, call.market)
			if len(chunk) == 0 {
				return
			}
			rowsMu.Lock()
			rows = append(rows, chunk...)
			reachable[call.exchange] = struct{}{}
			rowsMu.Unlock()
		}()
	}
	wg.Wait()

	if hasDex {
		hints := buildDexHints(rows)
		if contains(markets, "spot") {
			dexRows := p.fetchDexscreener(ctx, hints)
			if len(dexRows) > 0 {
				rows = append(rows, dexRows...)
				reachable["Dexscreener"] = struct{}{}
			}
		}
	}

	return rows, reachable
}

func (p *Provider) fetchExchange(ctx context.Context, exchange, market string) []entity.Quote {
	switch exchange {
	case "Binance":
		return p.fetchBinance(ctx, market)
	case "OKX":
		return p.fetchOKX(ctx, market)
	case "Bybit":
		return p.fetchBybit(ctx, market)
	case "MEXC":
		return p.fetchMEXC(ctx, market)
	case "BingX":
		return p.fetchBingX(ctx, market)
	case "KuCoin":
		return p.fetchKuCoin(ctx, market)
	case "Gate.io":
		return p.fetchGate(ctx, market)
	case "Hyperliquid":
		return p.fetchHyperliquid(ctx, market)
	default:
		return []entity.Quote{}
	}
}

func (p *Provider) fetchBinance(ctx context.Context, market string) []entity.Quote {
	if market == "spot" {
		active := p.getBinanceActiveSpot(ctx)
		var arr []map[string]any
		if !p.fetchJSON(ctx, "GET", "https://api.binance.com/api/v3/ticker/bookTicker", nil, &arr) {
			return nil
		}
		out := make([]entity.Quote, 0, len(arr))
		for _, row := range arr {
			symbol := shared.NormalizeSymbol(toString(row["symbol"]))
			if symbol == "" || !isActive(active, symbol) {
				continue
			}
			q := mkRow("Binance", symbol, "spot", toFloat(row["bidPrice"]), toFloat(row["askPrice"]), 0, nil, nil, nil)
			if q != nil {
				out = append(out, *q)
			}
		}
		return p.capSymbols(out)
	}

	active := p.getBinanceActiveFutures(ctx)
	var tickers []map[string]any
	var premium []map[string]any
	if !p.fetchJSON(ctx, "GET", "https://fapi.binance.com/fapi/v1/ticker/bookTicker", nil, &tickers) {
		return nil
	}
	_ = p.fetchJSON(ctx, "GET", "https://fapi.binance.com/fapi/v1/premiumIndex", nil, &premium)

	funding := make(map[string]fundingValue)
	for _, row := range premium {
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol == "" {
			continue
		}
		rate := ptrFloat(toFloat(row["lastFundingRate"]))
		next := ptrInt64(toInt64(row["nextFundingTime"]))
		funding[symbol] = fundingValue{Rate: rate, NextTS: next}
	}

	out := make([]entity.Quote, 0, len(tickers))
	for _, row := range tickers {
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol == "" || !isActive(active, symbol) {
			continue
		}
		fv := funding[symbol]
		q := mkRow("Binance", symbol, "futures", toFloat(row["bidPrice"]), toFloat(row["askPrice"]), 0, fv.Rate, fv.NextTS, nil)
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) fetchOKX(ctx context.Context, market string) []entity.Quote {
	instType := "SPOT"
	if market == "futures" {
		instType = "SWAP"
	}
	active := p.getOKXActive(ctx, market)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://www.okx.com/api/v5/market/tickers?instType="+instType, nil, &resp) {
		return nil
	}

	candidates := make([]map[string]any, 0, len(resp.Data))
	for _, row := range resp.Data {
		instID := toString(row["instId"])
		symbol := shared.NormalizeOKXSymbol(instID)
		if symbol == "" || !isActive(active, symbol) {
			continue
		}
		bid := toFloat(row["bidPx"])
		ask := toFloat(row["askPx"])
		if bid <= 0 || ask <= 0 {
			continue
		}
		candidates = append(candidates, map[string]any{
			"instId": instID,
			"symbol": symbol,
			"bid":    bid,
			"ask":    ask,
			"vol":    toFloat(row["volCcy24h"]),
			"url":    fmt.Sprintf("https://www.okx.com/%s/%s", ternary(market == "futures", "trade-swap", "trade-spot"), strings.ToLower(instID)),
		})
	}

	candidates = p.capSymbolsMap(candidates)
	if market != "futures" {
		out := make([]entity.Quote, 0, len(candidates))
		for _, c := range candidates {
			q := mkRow("OKX", toString(c["symbol"]), "spot", toFloat(c["bid"]), toFloat(c["ask"]), toFloat(c["vol"]), nil, nil, ptrString(toString(c["url"])))
			if q != nil {
				out = append(out, *q)
			}
		}
		return out
	}

	instIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		instIDs = append(instIDs, toString(c["instId"]))
	}
	funding := p.fetchOKXFunding(ctx, instIDs)
	out := make([]entity.Quote, 0, len(candidates))
	for _, c := range candidates {
		fv := funding[toString(c["instId"])]
		q := mkRow("OKX", toString(c["symbol"]), "futures", toFloat(c["bid"]), toFloat(c["ask"]), toFloat(c["vol"]), fv.Rate, fv.NextTS, ptrString(toString(c["url"])))
		if q != nil {
			out = append(out, *q)
		}
	}
	return out
}

func (p *Provider) fetchBybit(ctx context.Context, market string) []entity.Quote {
	category := "spot"
	if market == "futures" {
		category = "linear"
	}
	active := p.getBybitActive(ctx, market)
	var resp struct {
		Result struct {
			List []map[string]any `json:"list"`
		} `json:"result"`
	}
	if !p.fetchJSON(ctx, "GET", "https://api.bybit.com/v5/market/tickers?category="+category, nil, &resp) {
		return nil
	}

	out := make([]entity.Quote, 0, len(resp.Result.List))
	for _, row := range resp.Result.List {
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol == "" || !isActive(active, symbol) {
			continue
		}
		var fundingRate *float64
		var fundingNext *int64
		if market == "futures" {
			fundingRate = ptrFloat(toFloat(row["fundingRate"]))
			fundingNext = ptrInt64(toInt64(row["nextFundingTime"]))
		}
		q := mkRow("Bybit", symbol, market, toFloat(row["bid1Price"]), toFloat(row["ask1Price"]), toFloat(row["turnover24h"]), fundingRate, fundingNext, nil)
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) fetchMEXC(ctx context.Context, market string) []entity.Quote {
	if market == "spot" {
		active := p.getMEXCActiveSpot(ctx)
		var arr []map[string]any
		if !p.fetchJSON(ctx, "GET", "https://api.mexc.com/api/v3/ticker/bookTicker", nil, &arr) {
			return nil
		}
		out := make([]entity.Quote, 0, len(arr))
		for _, row := range arr {
			symbol := shared.NormalizeSymbol(toString(row["symbol"]))
			if symbol == "" || !isActive(active, symbol) {
				continue
			}
			q := mkRow("MEXC", symbol, "spot", toFloat(row["bidPrice"]), toFloat(row["askPrice"]), 0, nil, nil, nil)
			if q != nil {
				out = append(out, *q)
			}
		}
		return p.capSymbols(out)
	}

	activeContracts := p.getMEXCActiveFutures(ctx)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://contract.mexc.com/api/v1/contract/ticker", nil, &resp) {
		return nil
	}
	candidates := make([]map[string]any, 0, len(resp.Data))
	for _, row := range resp.Data {
		contractSymbol := toString(row["symbol"])
		symbol := shared.NormalizeSymbol(contractSymbol)
		if symbol == "" || !isActive(activeContracts, contractSymbol) {
			continue
		}
		bid := toFloat(row["bid1"])
		ask := toFloat(row["ask1"])
		if bid <= 0 || ask <= 0 {
			continue
		}
		candidates = append(candidates, map[string]any{
			"contractSymbol": contractSymbol,
			"symbol":         symbol,
			"bid":            bid,
			"ask":            ask,
			"vol":            toFloat(coalesce(row["amount24"], row["volume24"])),
			"fundingRate":    toFloat(row["fundingRate"]),
		})
	}
	candidates = p.capSymbolsMap(candidates)

	contractSymbols := make([]string, 0, len(candidates))
	for _, c := range candidates {
		contractSymbols = append(contractSymbols, toString(c["contractSymbol"]))
	}
	fundingMap := p.fetchMEXCFunding(ctx, contractSymbols)
	out := make([]entity.Quote, 0, len(candidates))
	for _, c := range candidates {
		fv := fundingMap[toString(c["contractSymbol"])]
		rate := fv.Rate
		if rate == nil {
			rate = ptrFloat(toFloat(c["fundingRate"]))
		}
		q := mkRow("MEXC", toString(c["symbol"]), "futures", toFloat(c["bid"]), toFloat(c["ask"]), toFloat(c["vol"]), rate, fv.NextTS, nil)
		if q != nil {
			out = append(out, *q)
		}
	}
	return out
}

func (p *Provider) fetchBingX(ctx context.Context, market string) []entity.Quote {
	if market == "spot" {
		active := p.getBingXActiveSpot(ctx)
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if !p.fetchJSON(ctx, "GET", "https://open-api.bingx.com/openApi/spot/v1/ticker/bookTicker", nil, &resp) {
			return nil
		}
		out := make([]entity.Quote, 0, len(resp.Data))
		for _, row := range resp.Data {
			symbol := shared.NormalizeSymbol(toString(row["symbol"]))
			if symbol == "" || !isActive(active, symbol) {
				continue
			}
			q := mkRow("BingX", symbol, "spot", toFloat(row["bidPrice"]), toFloat(row["askPrice"]), 0, nil, nil, nil)
			if q != nil {
				out = append(out, *q)
			}
		}
		return p.capSymbols(out)
	}

	active := p.getBingXActiveFutures(ctx)
	var tickerResp struct {
		Data []map[string]any `json:"data"`
	}
	var premiumResp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://open-api.bingx.com/openApi/swap/v2/quote/ticker", nil, &tickerResp) {
		return nil
	}
	_ = p.fetchJSON(ctx, "GET", "https://open-api.bingx.com/openApi/swap/v2/quote/premiumIndex", nil, &premiumResp)
	fundingBySymbol := make(map[string]fundingValue)
	for _, row := range premiumResp.Data {
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol == "" {
			continue
		}
		fundingBySymbol[symbol] = fundingValue{
			Rate:   ptrFloat(toFloat(coalesce(row["lastFundingRate"], row["fundingRate"], row["estimatedSettlePriceRate"]))),
			NextTS: ptrInt64(toInt64(coalesce(row["nextFundingTime"], row["nextSettleTime"], row["settleTime"]))),
		}
	}

	out := make([]entity.Quote, 0, len(tickerResp.Data))
	for _, row := range tickerResp.Data {
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol == "" || !isActive(active, symbol) {
			continue
		}
		fv := fundingBySymbol[symbol]
		url := "https://bingx.com/en-us/futures/forward/" + symbol
		q := mkRow("BingX", symbol, "futures", toFloat(coalesce(row["bidPrice"], row["bid"], row["bestBidPrice"])), toFloat(coalesce(row["askPrice"], row["ask"], row["bestAskPrice"])), toFloat(coalesce(row["quoteVolume"], row["turnover"], row["volume"], row["amount"])), fv.Rate, fv.NextTS, &url)
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) getBingXActiveSpot(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("BingX", "spot", 300000); set != nil {
		return set
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://open-api.bingx.com/openApi/spot/v1/common/symbols", nil, &resp) {
		return nil
	}
	rawRows, _ := resp.Data["symbols"].([]any)
	set := make(map[string]struct{})
	for _, row := range rawRows {
		item, _ := row.(map[string]any)
		status := strings.ToLower(toString(coalesce(item["status"], item["state"], item["symbolStatus"])))
		if status != "" && status != "1" && status != "enabled" && status != "online" && status != "trading" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(coalesce(item["symbol"], item["symbolName"])))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("BingX", "spot", set)
	return set
}

func (p *Provider) getBingXActiveFutures(ctx context.Context) map[string]struct{} {
	if set := p.getActiveCache("BingX", "futures", 300000); set != nil {
		return set
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://open-api.bingx.com/openApi/swap/v2/quote/contracts", nil, &resp) {
		return nil
	}
	set := make(map[string]struct{})
	for _, row := range resp.Data {
		status := strings.ToLower(toString(coalesce(row["status"], row["symbolStatus"], row["state"])))
		if status != "" && status != "1" && status != "enabled" && status != "online" && status != "trading" && status != "listed" {
			continue
		}
		symbol := shared.NormalizeSymbol(toString(row["symbol"]))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	p.setActiveCache("BingX", "futures", set)
	return set
}

func (p *Provider) fetchKuCoin(ctx context.Context, market string) []entity.Quote {
	if market == "spot" {
		active := p.getKucoinActiveSpot(ctx)
		var resp struct {
			Data struct {
				Ticker []map[string]any `json:"ticker"`
			} `json:"data"`
		}
		if !p.fetchJSON(ctx, "GET", "https://api.kucoin.com/api/v1/market/allTickers", nil, &resp) {
			return nil
		}
		out := make([]entity.Quote, 0, len(resp.Data.Ticker))
		for _, row := range resp.Data.Ticker {
			symbol := shared.NormalizeSymbol(toString(row["symbol"]))
			if symbol == "" || !isActive(active, symbol) {
				continue
			}
			pairRaw := strings.ToUpper(strings.TrimSpace(toString(row["symbol"])))
			url := ""
			if pairRaw != "" {
				url = "https://www.kucoin.com/trade/" + pairRaw
			}
			q := mkRow("KuCoin", symbol, "spot", toFloat(row["buy"]), toFloat(row["sell"]), toFloat(coalesce(row["volValue"], row["vol"])), nil, nil, ptrString(url))
			if q != nil {
				out = append(out, *q)
			}
		}
		return p.capSymbols(out)
	}

	var tickerResp struct {
		Data []map[string]any `json:"data"`
	}
	var contractsResp struct {
		Data []map[string]any `json:"data"`
	}
	if !p.fetchJSON(ctx, "GET", "https://api-futures.kucoin.com/api/v1/allTickers", nil, &tickerResp) {
		return nil
	}
	if !p.fetchJSON(ctx, "GET", "https://api-futures.kucoin.com/api/v1/contracts/active", nil, &contractsResp) {
		return nil
	}

	metaByContract := make(map[string]map[string]any)
	for _, row := range contractsResp.Data {
		contractSymbol := toString(row["symbol"])
		if contractSymbol == "" {
			continue
		}
		metaByContract[contractSymbol] = row
	}

	out := make([]entity.Quote, 0, len(tickerResp.Data))
	for _, ticker := range tickerResp.Data {
		contractSymbol := toString(ticker["symbol"])
		meta := metaByContract[contractSymbol]
		symbol := shared.NormalizeKucoinFuturesSymbol(contractSymbol, toString(meta["baseCurrency"]), toString(meta["quoteCurrency"]))
		if symbol == "" {
			continue
		}
		bid := toFloat(ticker["bestBidPrice"])
		ask := toFloat(ticker["bestAskPrice"])
		last := toFloat(ticker["price"])
		if (bid <= 0 || ask <= 0) && last > 0 {
			spread := last * 0.0002
			if bid <= 0 {
				bid = last - spread
			}
			if ask <= 0 {
				ask = last + spread
			}
		}
		fundingTS := normalizeKucoinFundingNextTS(coalesce(meta["nextFundingRateTime"], meta["nextFundingTime"], meta["nextFundingRateDateTime"]))
		url := "https://www.kucoin.com/futures/trade/" + contractSymbol
		q := mkRow("KuCoin", symbol, "futures", bid, ask, toFloat(coalesce(meta["turnoverOf24h"], meta["volumeOf24h"])), ptrFloat(toFloat(coalesce(meta["fundingFeeRate"], meta["fundingRate"]))), fundingTS, &url)
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) fetchGate(ctx context.Context, market string) []entity.Quote {
	if market == "spot" {
		active := p.getGateActiveSpot(ctx)
		var arr []map[string]any
		if !p.fetchJSON(ctx, "GET", "https://api.gateio.ws/api/v4/spot/tickers", nil, &arr) {
			return nil
		}
		out := make([]entity.Quote, 0, len(arr))
		for _, row := range arr {
			symbol := shared.NormalizeSymbol(toString(row["currency_pair"]))
			if symbol == "" || !isActive(active, symbol) {
				continue
			}
			q := mkRow("Gate.io", symbol, "spot", toFloat(row["highest_bid"]), toFloat(row["lowest_ask"]), toFloat(coalesce(row["quote_volume"], row["base_volume"], row["volume"])), nil, nil, nil)
			if q != nil {
				out = append(out, *q)
			}
		}
		return p.capSymbols(out)
	}

	active := p.getGateActiveFutures(ctx)
	var arr []map[string]any
	if !p.fetchJSON(ctx, "GET", "https://api.gateio.ws/api/v4/futures/usdt/tickers", nil, &arr) {
		return nil
	}
	out := make([]entity.Quote, 0, len(arr))
	for _, row := range arr {
		symbol := shared.NormalizeSymbol(toString(row["contract"]))
		if symbol == "" || !isActive(active, symbol) {
			continue
		}
		q := mkRow("Gate.io", symbol, "futures", toFloat(row["highest_bid"]), toFloat(row["lowest_ask"]), toFloat(coalesce(row["volume_24h_quote"], row["volume_24h_settle"], row["volume_24h_base"], row["volume_24h_usd"], row["volume_24h"])), ptrFloat(toFloat(row["funding_rate"])), ptrInt64(toInt64(row["funding_next_apply"])), nil)
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) fetchHyperliquid(ctx context.Context, market string) []entity.Quote {
	if market == "futures" {
		var metaAndCtx []any
		var mids map[string]any
		if !p.fetchJSON(ctx, "POST", "https://api.hyperliquid.xyz/info", map[string]string{"Content-Type": "application/json"}, &metaAndCtx, map[string]any{"type": "metaAndAssetCtxs"}) {
			return nil
		}
		if !p.fetchJSON(ctx, "POST", "https://api.hyperliquid.xyz/info", map[string]string{"Content-Type": "application/json"}, &mids, map[string]any{"type": "allMids"}) {
			return nil
		}
		if len(metaAndCtx) < 2 {
			return nil
		}
		meta, _ := metaAndCtx[0].(map[string]any)
		universe, _ := meta["universe"].([]any)
		ctxs, _ := metaAndCtx[1].([]any)
		if len(universe) == 0 || len(ctxs) == 0 {
			return nil
		}
		out := make([]entity.Quote, 0, len(universe))
		for i := 0; i < len(universe); i++ {
			u, _ := universe[i].(map[string]any)
			ctxRow, _ := ctxs[i].(map[string]any)
			coin := toString(u["name"])
			mid := toFloat(mids[coin])
			if coin == "" || mid <= 0 {
				continue
			}
			symbol := shared.NormalizeSymbol(coin + "USDT")
			q := mkRow("Hyperliquid", symbol, "futures", mid*0.9998, mid*1.0002, toFloat(ctxRow["dayNtlVlm"]), ptrFloat(toFloat(coalesce(ctxRow["funding"], ctxRow["fundingRate"]))), ptrInt64(toInt64(coalesce(ctxRow["nextFundingTime"], ctxRow["nextFundingTs"]))), nil)
			if q != nil {
				out = append(out, *q)
			}
		}
		return p.capSymbols(out)
	}

	var spotMeta []any
	if !p.fetchJSON(ctx, "POST", "https://api.hyperliquid.xyz/info", map[string]string{"Content-Type": "application/json"}, &spotMeta, map[string]any{"type": "spotMetaAndAssetCtxs"}) {
		return nil
	}
	if len(spotMeta) < 2 {
		return nil
	}
	meta, _ := spotMeta[0].(map[string]any)
	ctxs, _ := spotMeta[1].([]any)
	universe, _ := meta["universe"].([]any)
	tokens, _ := meta["tokens"].([]any)
	if len(universe) == 0 || len(tokens) == 0 {
		return nil
	}
	tokenByIndex := make(map[int]string)
	for _, t := range tokens {
		row, _ := t.(map[string]any)
		idx := int(toInt64(row["index"]))
		name := strings.ToUpper(strings.TrimSpace(toString(row["name"])))
		if name != "" {
			tokenByIndex[idx] = name
		}
	}
	out := make([]entity.Quote, 0, len(universe))
	for i, pRow := range universe {
		pair, _ := pRow.(map[string]any)
		idxRows, _ := pair["tokens"].([]any)
		if len(idxRows) < 2 {
			continue
		}
		base := tokenByIndex[int(toInt64(idxRows[0]))]
		quote := tokenByIndex[int(toInt64(idxRows[1]))]
		if base == "" || (quote != "USDT" && quote != "USDC") {
			continue
		}
		symbol := shared.NormalizeSymbol(base + quote)
		ctxRow, _ := ctxs[i].(map[string]any)
		mid := toFloat(coalesce(ctxRow["midPx"], ctxRow["markPx"]))
		if symbol == "" || mid <= 0 {
			continue
		}
		q := mkRow("Hyperliquid", symbol, "spot", mid*0.9998, mid*1.0002, toFloat(ctxRow["dayNtlVlm"]), nil, nil, nil)
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) fetchDexscreener(ctx context.Context, baseHints []string) []entity.Quote {
	hintPool := uniqueStrings(baseHints)
	fallback := []string{"BTC", "ETH", "SOL", "XRP", "ADA", "DOGE", "BNB", "TRX", "SUI", "TON", "AVAX", "LINK"}
	bases := make([]string, 0, 24)
	bases = append(bases, hintPool...)
	for _, x := range fallback {
		if !containsString(bases, x) {
			bases = append(bases, x)
		}
	}
	maxQueries := p.cfg.DexMaxTokens
	if maxQueries <= 0 {
		maxQueries = 24
	}
	if maxQueries > 24 {
		maxQueries = 24
	}
	if len(bases) > maxQueries {
		bases = bases[:maxQueries]
	}

	bySymbol := make(map[string]map[string]any)
	for _, base := range bases {
		url := "https://api.dexscreener.com/latest/dex/search?q=" + base + "%2FUSDT"
		var resp struct {
			Pairs []map[string]any `json:"pairs"`
		}
		if !p.fetchJSON(ctx, "GET", url, nil, &resp) {
			continue
		}
		for _, pair := range resp.Pairs {
			baseToken, _ := pair["baseToken"].(map[string]any)
			quoteToken, _ := pair["quoteToken"].(map[string]any)
			baseS := sanitizeSymbol(toString(baseToken["symbol"]))
			quoteS := sanitizeSymbol(toString(quoteToken["symbol"]))
			if baseS == "" || (quoteS != "USDT" && quoteS != "USDC") {
				continue
			}
			symbol := shared.NormalizeSymbol(baseS + quoteS)
			price := toFloat(pair["priceUsd"])
			if symbol == "" || price <= 0 {
				continue
			}
			liquidity, _ := pair["liquidity"].(map[string]any)
			volume, _ := pair["volume"].(map[string]any)
			liquidityUSD := toFloat(liquidity["usd"])
			existing := bySymbol[symbol]
			if existing != nil && toFloat(existing["liquidityUsd"]) >= liquidityUSD {
				continue
			}
			tradeURL := toString(pair["url"])
			if tradeURL != "" && !strings.HasPrefix(tradeURL, "http") {
				tradeURL = "https://dexscreener.com" + tradeURL
			}
			bySymbol[symbol] = map[string]any{
				"symbol":       symbol,
				"price":        price,
				"volume24h":    toFloat(volume["h24"]),
				"liquidityUsd": liquidityUSD,
				"tradeUrl":     tradeURL,
			}
		}
	}

	out := make([]entity.Quote, 0, len(bySymbol))
	for _, row := range bySymbol {
		price := toFloat(row["price"])
		spread := price * 0.001
		tradeURL := toString(row["tradeUrl"])
		q := mkRow("Dexscreener", toString(row["symbol"]), "spot", price-spread, price+spread, toFloat(row["volume24h"]), nil, nil, ptrString(tradeURL))
		if q != nil {
			out = append(out, *q)
		}
	}
	return p.capSymbols(out)
}

func (p *Provider) fetchOKXFunding(ctx context.Context, instIDs []string) map[string]fundingValue {
	instIDs = uniqueStrings(instIDs)
	if len(instIDs) == 0 {
		return map[string]fundingValue{}
	}
	capN := p.cfg.MaxFundingSymbols
	if capN <= 0 {
		capN = 80
	}
	if len(instIDs) > capN {
		instIDs = instIDs[:capN]
	}
	now := time.Now().UnixMilli()
	out := make(map[string]fundingValue, len(instIDs))
	missing := make([]string, 0, len(instIDs))

	p.okxFundingMu.Lock()
	for _, id := range instIDs {
		v, ok := p.okxFunding[id]
		if ok && now-v.TS < 120000 {
			out[id] = v
			continue
		}
		missing = append(missing, id)
	}
	p.okxFundingMu.Unlock()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var outMu sync.Mutex
	for _, instID := range missing {
		instID := instID
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var resp struct {
				Data []map[string]any `json:"data"`
			}
			url := "https://www.okx.com/api/v5/public/funding-rate?instId=" + instID
			if !p.fetchJSON(ctx, "GET", url, nil, &resp) || len(resp.Data) == 0 {
				return
			}
			row := resp.Data[0]
			fv := fundingValue{Rate: ptrFloat(toFloat(row["fundingRate"])), NextTS: ptrInt64(toInt64(row["nextFundingTime"])), TS: time.Now().UnixMilli()}
			p.okxFundingMu.Lock()
			p.okxFunding[instID] = fv
			p.okxFundingMu.Unlock()
			outMu.Lock()
			out[instID] = fv
			outMu.Unlock()
		}()
	}
	wg.Wait()

	for _, id := range instIDs {
		if _, ok := out[id]; ok {
			continue
		}
		p.okxFundingMu.Lock()
		v, ok := p.okxFunding[id]
		p.okxFundingMu.Unlock()
		if ok {
			out[id] = v
		} else {
			out[id] = fundingValue{}
		}
	}
	return out
}

func (p *Provider) fetchMEXCFunding(ctx context.Context, contractSymbols []string) map[string]fundingValue {
	contractSymbols = uniqueStrings(contractSymbols)
	if len(contractSymbols) == 0 {
		return map[string]fundingValue{}
	}
	capN := p.cfg.MaxFundingSymbols
	if capN <= 0 {
		capN = 80
	}
	if len(contractSymbols) > capN {
		contractSymbols = contractSymbols[:capN]
	}
	now := time.Now().UnixMilli()
	out := make(map[string]fundingValue, len(contractSymbols))
	missing := make([]string, 0, len(contractSymbols))

	p.mexcFundMu.Lock()
	for _, s := range contractSymbols {
		v, ok := p.mexcFunding[s]
		if ok && now-v.TS < 120000 {
			out[s] = v
			continue
		}
		missing = append(missing, s)
	}
	p.mexcFundMu.Unlock()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var outMu sync.Mutex
	for _, contract := range missing {
		contract := contract
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var resp struct {
				Data map[string]any `json:"data"`
			}
			url := "https://contract.mexc.com/api/v1/contract/funding_rate/" + contract
			if !p.fetchJSON(ctx, "GET", url, nil, &resp) {
				return
			}
			row := resp.Data
			fv := fundingValue{Rate: ptrFloat(toFloat(row["fundingRate"])), NextTS: ptrInt64(toInt64(coalesce(row["nextSettleTime"], row["nextFundingTime"]))), TS: time.Now().UnixMilli()}
			p.mexcFundMu.Lock()
			p.mexcFunding[contract] = fv
			p.mexcFundMu.Unlock()
			outMu.Lock()
			out[contract] = fv
			outMu.Unlock()
		}()
	}
	wg.Wait()

	for _, s := range contractSymbols {
		if _, ok := out[s]; ok {
			continue
		}
		p.mexcFundMu.Lock()
		v, ok := p.mexcFunding[s]
		p.mexcFundMu.Unlock()
		if ok {
			out[s] = v
		} else {
			out[s] = fundingValue{}
		}
	}
	return out
}

func (p *Provider) fetchJSON(ctx context.Context, method, url string, headers map[string]string, out any, body ...any) bool {
	var payload io.Reader
	if len(body) > 0 {
		raw, err := json.Marshal(body[0])
		if err != nil {
			return false
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return false
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if payload != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	decoder := json.NewDecoder(resp.Body)
	return decoder.Decode(out) == nil
}

func (p *Provider) capSymbols(rows []entity.Quote) []entity.Quote {
	if p.cfg.MaxSymbolsPerExchange == nil {
		return rows
	}
	maxSymbols := *p.cfg.MaxSymbolsPerExchange
	if maxSymbols <= 0 {
		return rows
	}
	seen := make(map[string]struct{})
	out := make([]entity.Quote, 0, len(rows))
	for _, row := range rows {
		_, exists := seen[row.Symbol]
		if !exists && len(seen) >= maxSymbols {
			continue
		}
		seen[row.Symbol] = struct{}{}
		out = append(out, row)
	}
	return out
}

func (p *Provider) capSymbolsMap(rows []map[string]any) []map[string]any {
	if p.cfg.MaxSymbolsPerExchange == nil {
		return rows
	}
	maxSymbols := *p.cfg.MaxSymbolsPerExchange
	if maxSymbols <= 0 {
		return rows
	}
	seen := make(map[string]struct{})
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		symbol := toString(row["symbol"])
		_, exists := seen[symbol]
		if !exists && len(seen) >= maxSymbols {
			continue
		}
		seen[symbol] = struct{}{}
		out = append(out, row)
	}
	return out
}

func (p *Provider) mockFallback(mode string) []entity.Quote {
	markets := modeToMarkets(mode)
	out := make([]entity.Quote, 0)
	for _, exchange := range p.cfg.Exchanges {
		for _, market := range markets {
			for _, symbol := range coreSymbols {
				base := basePrice(symbol)
				px := jitter(base*exchangeBias(exchange), ternaryFloat(market == "futures", 0.004, 0.003))
				spread := px * ((rand.Float64() * 0.0008) + 0.0001)
				q := mkRow(exchange, symbol, market, px-spread, px+spread, math.Round(rand.Float64()*500_000_000), nil, nil, nil)
				if q != nil {
					q.IsMock = true
					out = append(out, *q)
				}
			}
		}
	}
	return out
}

func mkRow(exchange, symbol, market string, bid, ask, volume float64, fundingRate *float64, fundingNextTS *int64, tradeURL *string) *entity.Quote {
	if symbol == "" || bid <= 0 || ask <= 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	row := &entity.Quote{
		Exchange:      exchange,
		Symbol:        symbol,
		Market:        market,
		Bid:           bid,
		Ask:           ask,
		Volume24h:     volume,
		Timestamp:     now,
		FundingRate:   fundingRate,
		FundingNextTs: fundingNextTS,
		TradeURL:      tradeURL,
		Networks:      networks,
	}
	return row
}

func modeToMarkets(mode string) []string {
	if mode == "spot-futures" {
		return []string{"spot", "futures"}
	}
	return []string{mode}
}

func buildDexHints(rows []entity.Quote) []string {
	scoreByBase := make(map[string]float64)
	for _, row := range rows {
		if row.Exchange == "Dexscreener" {
			continue
		}
		if row.Market != "spot" && row.Market != "futures" {
			continue
		}
		base := shared.SymbolBase(row.Symbol)
		if base == "" {
			continue
		}
		if row.Volume24h > scoreByBase[base] {
			scoreByBase[base] = row.Volume24h
		}
	}
	list := make([]string, 0, len(scoreByBase))
	for base := range scoreByBase {
		list = append(list, base)
	}
	sort.Slice(list, func(i, j int) bool {
		return scoreByBase[list[i]] > scoreByBase[list[j]]
	})
	return list
}
