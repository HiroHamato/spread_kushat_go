package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Host                    string
	Port                    int
	RefreshMS               int
	ProviderTimeoutMS       int
	ProviderExchangeTimeout int
	TGMenuRefreshMS         int
	UseMockFallback         bool
	DexMaxTokens            int
	MaxFundingSymbols       int
	MaxSymbolsPerExchange   *int
	DefaultMode             string
	MinSpreadPercent        float64
	TradingFeePerSide       float64
	WithdrawFeePercent      float64
	NetworkFeePercent       float64
	Telegram                TelegramConfig
	Exchanges               []string
	DB                      DBConfig
	Redis                   RedisConfig
}

type TelegramConfig struct {
	BotToken string
	ChatIDs  []string
}

type DBConfig struct {
	URL string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

func Load() Config {
	_ = godotenv.Load()

	chatIDs := make([]string, 0)
	for _, v := range strings.Split(os.Getenv("TG_CHAT_IDS"), ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			chatIDs = append(chatIDs, v)
		}
	}

	return Config{
		Host:                    env("HOST", "0.0.0.0"),
		Port:                    envInt("PORT", 3000),
		RefreshMS:               envPositiveInt("REFRESH_MS", 1000),
		ProviderTimeoutMS:       envPositiveInt("PROVIDER_TIMEOUT_MS", 4500),
		ProviderExchangeTimeout: envPositiveInt("PROVIDER_EXCHANGE_TIMEOUT_MS", 9000),
		TGMenuRefreshMS:         maxInt(1000, envPositiveInt("TG_MENU_REFRESH_MS", 1000)),
		UseMockFallback:         envBool("USE_MOCK_FALLBACK", true),
		DexMaxTokens:            envPositiveInt("DEXSCREENER_MAX_TOKENS", 30),
		MaxFundingSymbols:       envPositiveInt("MAX_FUNDING_SYMBOLS_PER_EXCHANGE", 80),
		MaxSymbolsPerExchange:   envNullableInt("MAX_SYMBOLS_PER_EXCHANGE"),
		DefaultMode:             "spot-futures",
		MinSpreadPercent:        0.5,
		TradingFeePerSide:       0.1,
		WithdrawFeePercent:      0.05,
		NetworkFeePercent:       0.03,
		Telegram: TelegramConfig{
			BotToken: env("TG_BOT_TOKEN", ""),
			ChatIDs:  chatIDs,
		},
		Exchanges: []string{"MEXC", "BingX", "KuCoin", "Gate.io", "OKX", "Binance", "Bybit", "Hyperliquid", "Dexscreener"},
		DB: DBConfig{
			URL: env("DATABASE_URL", "postgres://postgres:postgres@postgres:5432/spread_bot?sslmode=disable"),
		},
		Redis: RedisConfig{
			Addr:     env("REDIS_ADDR", "redis:6379"),
			Password: env("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
		},
	}
}

func env(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func envPositiveInt(key string, fallback int) int {
	n := envInt(key, fallback)
	if n <= 0 {
		return fallback
	}
	return n
}

func envNullableInt(key string) *int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	if raw == "1" || raw == "true" || raw == "yes" || raw == "on" {
		return true
	}
	if raw == "0" || raw == "false" || raw == "no" || raw == "off" {
		return false
	}
	return fallback
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
