package service

import (
	"math"
	"sort"
	"strings"
	"time"

	"spread_kushat_pora_golang/internal/domain/entity"
)

const (
	TrendMinPoints         = 6
	TrendEpsilon           = 0.06
	TrendReversalAllowance = 0.2
	TrendMinChange         = 0.35
	TrendMinDurationMS     = int64(2 * 60 * 1000)
	TrendSmoothAlpha       = 0.35
)

type FeeConfig struct {
	TradingPercentPerSide float64
	WithdrawPercent       float64
	NetworkPercent        float64
}

func CalcSpreadPercent(buyAsk, sellBid float64) float64 {
	if buyAsk <= 0 || sellBid <= 0 {
		return 0
	}
	return ((sellBid - buyAsk) / buyAsk) * 100
}

func CalcNetSpreadPercent(spreadPercent float64, fees FeeConfig) float64 {
	totalFees := (fees.TradingPercentPerSide * 2) + fees.WithdrawPercent + fees.NetworkPercent
	return spreadPercent - totalFees
}

func BuildOpportunity(pair, market, buyMarket, sellMarket string, buy, sell entity.Quote, nowMS int64, minSpread float64, fees FeeConfig) *entity.Opportunity {
	if buy.Exchange == "" || sell.Exchange == "" || buy.Exchange == sell.Exchange {
		return nil
	}

	spreadPercent := CalcSpreadPercent(buy.Ask, sell.Bid)
	if spreadPercent < minSpread {
		return nil
	}

	netSpread := CalcNetSpreadPercent(spreadPercent, fees)
	networks := CommonNetworks(buy.Networks, sell.Networks)

	op := &entity.Opportunity{
		ID:                pair + ":" + market + ":" + buy.Exchange + ":" + buyMarket + "->" + sell.Exchange + ":" + sellMarket,
		Pair:              pair,
		Market:            market,
		BuyMarket:         buyMarket,
		SellMarket:        sellMarket,
		BuyExchange:       buy.Exchange,
		SellExchange:      sell.Exchange,
		BuyTradeURL:       buy.TradeURL,
		SellTradeURL:      sell.TradeURL,
		BuyPrice:          buy.Ask,
		SellPrice:         sell.Bid,
		SpreadPercent:     spreadPercent,
		NetSpreadPercent:  netSpread,
		Volume24h:         ResolveOpportunityVolume(buy.Volume24h, sell.Volume24h),
		IsMock:            buy.IsMock || sell.IsMock,
		BuyFundingRate:    buy.FundingRate,
		BuyFundingNextTs:  buy.FundingNextTs,
		SellFundingRate:   sell.FundingRate,
		SellFundingNextTs: sell.FundingNextTs,
		UpdatedAt:         nowMS,
		CommonNetworks:    networks,
		HistoryKey:        MakeHistoryKey(pair, market, buy.Exchange, sell.Exchange, buyMarket, sellMarket),
	}
	return op
}

func MakeHistoryKey(pair, market, buyExchange, sellExchange, buyMarket, sellMarket string) string {
	return pair + "|" + market + "|" + buyExchange + "|" + buyMarket + "->" + sellExchange + "|" + sellMarket
}

func CommonNetworks(a, b []entity.Network) []string {
	withdraw := make(map[string]struct{})
	for _, n := range a {
		if n.Withdraw {
			withdraw[n.Name] = struct{}{}
		}
	}

	deposit := make(map[string]struct{})
	for _, n := range b {
		if n.Deposit {
			deposit[n.Name] = struct{}{}
		}
	}

	out := make([]string, 0)
	for name := range withdraw {
		if _, ok := deposit[name]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func ResolveOpportunityVolume(buyVolume, sellVolume float64) float64 {
	buyValid := buyVolume > 0
	sellValid := sellVolume > 0
	if buyValid && sellValid {
		if buyVolume < sellVolume {
			return buyVolume
		}
		return sellVolume
	}
	if buyValid {
		return buyVolume
	}
	if sellValid {
		return sellVolume
	}
	return 0
}

func AnalyzeHistory(history []entity.SpreadPoint, nowMS int64) entity.Trend {
	if len(history) < TrendMinPoints {
		current := 0.0
		if len(history) > 0 {
			current = history[len(history)-1].SpreadPercent
		}
		return entity.Trend{State: "insufficient", CurrentPercent: current}
	}

	smoothed := smoothSeries(history)
	direction := 0
	pivot := len(history) - 1
	reversalBudget := 0.0

	for i := len(smoothed) - 1; i > 0; i-- {
		delta := smoothed[i] - smoothed[i-1]
		if math.Abs(delta) < TrendEpsilon {
			continue
		}
		sign := -1
		if delta > 0 {
			sign = 1
		}

		if direction == 0 {
			direction = sign
			pivot = i - 1
			reversalBudget = 0
			continue
		}
		if sign == direction {
			pivot = i - 1
			reversalBudget = 0
			continue
		}
		reversalBudget += math.Abs(delta)
		if reversalBudget > TrendReversalAllowance {
			break
		}
	}

	if direction == 0 {
		last := history[len(history)-1]
		start := last.SpreadPercent
		ts := last.TS
		return entity.Trend{State: "stable", SinceTS: &ts, StartPercent: &start, CurrentPercent: start}
	}

	start := history[pivot]
	end := history[len(history)-1]
	durationMS := nowMS - start.TS
	if durationMS < 0 {
		durationMS = 0
	}
	change := end.SpreadPercent - start.SpreadPercent
	if math.Abs(change) < TrendMinChange || durationMS < TrendMinDurationMS {
		ts := start.TS
		startPct := start.SpreadPercent
		return entity.Trend{State: "stable", SinceTS: &ts, StartPercent: &startPct, CurrentPercent: end.SpreadPercent, DurationMS: durationMS, ChangePercent: change}
	}

	state := "diverging"
	if direction < 0 {
		state = "converging"
	}
	ts := start.TS
	startPct := start.SpreadPercent
	return entity.Trend{State: state, SinceTS: &ts, StartPercent: &startPct, CurrentPercent: end.SpreadPercent, DurationMS: durationMS, ChangePercent: change}
}

func smoothSeries(history []entity.SpreadPoint) []float64 {
	if len(history) == 0 {
		return nil
	}
	out := make([]float64, len(history))
	prev := history[0].SpreadPercent
	out[0] = prev
	for i := 1; i < len(history); i++ {
		value := history[i].SpreadPercent
		prev = (value * TrendSmoothAlpha) + (prev * (1 - TrendSmoothAlpha))
		out[i] = prev
	}
	return out
}

func GroupByPairMarket(quotes []entity.Quote) map[string][]entity.Quote {
	grouped := make(map[string][]entity.Quote)
	for _, q := range quotes {
		key := q.Symbol + ":" + q.Market
		grouped[key] = append(grouped[key], q)
	}
	return grouped
}

func GroupByPair(quotes []entity.Quote) map[string][]entity.Quote {
	grouped := make(map[string][]entity.Quote)
	for _, q := range quotes {
		grouped[q.Symbol] = append(grouped[q.Symbol], q)
	}
	return grouped
}

func FilterMarket(rows []entity.Quote, market string) []entity.Quote {
	out := make([]entity.Quote, 0, len(rows))
	for _, r := range rows {
		if strings.EqualFold(r.Market, market) {
			out = append(out, r)
		}
	}
	return out
}

func SortOpportunitiesByNetDesc(opps []entity.Opportunity) {
	sort.Slice(opps, func(i, j int) bool {
		return opps[i].NetSpreadPercent > opps[j].NetSpreadPercent
	})
}

func NowMS() int64 {
	return time.Now().UnixMilli()
}
