package main

import (
	"context"
	"log"
	"net"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"spread_kushat_pora_golang/internal/application/telegram"
	"spread_kushat_pora_golang/internal/application/watcher"
	"spread_kushat_pora_golang/internal/config"
	"spread_kushat_pora_golang/internal/infrastructure/httpserver"
	"spread_kushat_pora_golang/internal/infrastructure/providers"
	postgresrepo "spread_kushat_pora_golang/internal/infrastructure/storage/postgres"
	redisrepo "spread_kushat_pora_golang/internal/infrastructure/storage/redis"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pgPool, err := pgxpool.New(ctx, cfg.DB.URL)
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}
	defer pgPool.Close()

	if err := postgresrepo.RunMigrations(ctx, pgPool); err != nil {
		log.Fatalf("postgres migrations: %v", err)
	}

	redisClient := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer redisClient.Close()

	sessionRepo := postgresrepo.NewSessionRepository(pgPool)
	stateRepo := redisrepo.NewStateRepository(redisClient)
	quoteProvider := providers.NewProvider(cfg)
	alertManager := telegram.NewAlertManager(cfg)
	watchSvc := watcher.NewService(cfg, quoteProvider, stateRepo, alertManager)

	go watchSvc.Start(ctx)

	if cfg.Telegram.BotToken != "" {
		poller := telegram.NewPoller(cfg, sessionRepo, watchSvc, alertManager)
		go poller.Start(ctx)
		log.Printf("telegram polling started")
	} else {
		log.Printf("TG_BOT_TOKEN is missing. Telegram disabled")
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	httpSrv := httpserver.New(addr, watchSvc, cfg.Telegram.BotToken != "")
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			log.Printf("http server error: %v", err)
			stop()
		}
	}()
	log.Printf("health server on http://%s", addr)

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
