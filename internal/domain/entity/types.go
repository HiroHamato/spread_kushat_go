package entity

type Network struct {
	Name     string `json:"name"`
	Deposit  bool   `json:"deposit"`
	Withdraw bool   `json:"withdraw"`
}

type Quote struct {
	Exchange      string    `json:"exchange"`
	Symbol        string    `json:"symbol"`
	Market        string    `json:"market"`
	Bid           float64   `json:"bid"`
	Ask           float64   `json:"ask"`
	Volume24h     float64   `json:"volume24h"`
	Timestamp     int64     `json:"timestamp"`
	IsMock        bool      `json:"isMock"`
	FundingRate   *float64  `json:"fundingRate,omitempty"`
	FundingNextTs *int64    `json:"fundingNextTs,omitempty"`
	TradeURL      *string   `json:"tradeUrl,omitempty"`
	Networks      []Network `json:"networks"`
}

type Opportunity struct {
	ID                string   `json:"id"`
	Pair              string   `json:"pair"`
	Market            string   `json:"market"`
	BuyMarket         string   `json:"buyMarket"`
	SellMarket        string   `json:"sellMarket"`
	BuyExchange       string   `json:"buyExchange"`
	SellExchange      string   `json:"sellExchange"`
	BuyTradeURL       *string  `json:"buyTradeUrl,omitempty"`
	SellTradeURL      *string  `json:"sellTradeUrl,omitempty"`
	BuyPrice          float64  `json:"buyPrice"`
	SellPrice         float64  `json:"sellPrice"`
	SpreadPercent     float64  `json:"spreadPercent"`
	NetSpreadPercent  float64  `json:"netSpreadPercent"`
	Volume24h         float64  `json:"volume24h"`
	IsMock            bool     `json:"isMock"`
	BuyFundingRate    *float64 `json:"buyFundingRate,omitempty"`
	BuyFundingNextTs  *int64   `json:"buyFundingNextTs,omitempty"`
	SellFundingRate   *float64 `json:"sellFundingRate,omitempty"`
	SellFundingNextTs *int64   `json:"sellFundingNextTs,omitempty"`
	UpdatedAt         int64    `json:"updatedAt"`
	CommonNetworks    []string `json:"commonNetworks"`
	HistoryKey        string   `json:"historyKey"`
}

type SpreadPoint struct {
	TS               int64   `json:"ts"`
	SpreadPercent    float64 `json:"spreadPercent"`
	NetSpreadPercent float64 `json:"netSpreadPercent"`
}

type PricePoint struct {
	TS    int64   `json:"ts"`
	Price float64 `json:"price"`
}

type Trend struct {
	State          string   `json:"state"`
	SinceTS        *int64   `json:"sinceTs"`
	StartPercent   *float64 `json:"startPercent"`
	CurrentPercent float64  `json:"currentPercent"`
	DurationMS     int64    `json:"durationMs"`
	ChangePercent  float64  `json:"changePercent"`
}

type TrackedItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Pair         string `json:"pair"`
	Market       string `json:"market"`
	BuyExchange  string `json:"buyExchange"`
	BuyMarket    string `json:"buyMarket"`
	SellExchange string `json:"sellExchange"`
	SellMarket   string `json:"sellMarket"`
	CreatedAt    int64  `json:"createdAt"`
}

type Session struct {
	ChatID            string   `json:"chatId"`
	Modes             []string `json:"modes"`
	MinNetSpread      float64  `json:"minNetSpread"`
	MinVolume         float64  `json:"minVolume"`
	Page              int      `json:"page"`
	UIScreen          string   `json:"uiScreen"`
	AlertEnabled      bool     `json:"alertEnabled"`
	AlertThreshold    float64  `json:"alertThreshold"`
	AlertSpikeMinVol  float64  `json:"alertSpikeMinVol"`
	AlertSpikeModes   []string `json:"alertSpikeModes"`
	MenuMessageID     *int64   `json:"menuMessageId,omitempty"`
	LastRenderedState string   `json:"lastRenderedState"`
}

type WatcherSnapshot struct {
	UpdatedAt     int64         `json:"updatedAt"`
	Opportunities []Opportunity `json:"opportunities"`
}
