package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/user/whatsmeow-basileia/internal/api"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/database"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	redisinfra "github.com/user/whatsmeow-basileia/internal/infrastructure/redis"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/storage"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/internal/service/messaging"
	"github.com/user/whatsmeow-basileia/internal/service/webhook"
	"github.com/user/whatsmeow-basileia/pkg/config"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.uber.org/zap"
)

// @title WhatsMeow Basileia API
// @version 1.0
// @description API for WhatsApp automation using WhatsMeow.
// @BasePath /api

func main() {
	cfg := config.Load()
	logger.InitLogger(cfg.LogLevel)

	if err := cfg.Validate(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	logger.Info("Starting WhatsMeow Basileia Service...", zap.String("port", cfg.HTTPPort), zap.String("version", api.Version))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Database + migrations
	db, err := database.Open(ctx, cfg.DBDialect, cfg.DBAddress)
	if err != nil {
		logger.Error("Failed to connect to database", zap.Error(err))
		os.Exit(1)
	}
	defer db.Close()

	applied, err := database.Migrate(ctx, db)
	if err != nil {
		logger.Error("Database migration failed", zap.Error(err))
		os.Exit(1)
	}
	if len(applied) > 0 {
		logger.Info("Applied database migrations", zap.Strings("versions", applied))
	}

	// 2. Optional infrastructure: the service keeps running (degraded) if these are down.
	redisClient, err := redisinfra.New(ctx, cfg.RedisURL, cfg.RedisPass, cfg.RedisDB)
	if err != nil {
		logger.Error("Redis unavailable, continuing without it (rate limits and realtime fan-out are local only)", zap.Error(err))
		redisClient = nil
	}
	if redisClient != nil {
		defer redisClient.Close()
		logger.Info("Connected to Redis")
	}

	objectStore, err := storage.New(ctx, storage.Options{
		Endpoint:  cfg.MinioEndpoint,
		AccessKey: cfg.MinioAccessKey,
		SecretKey: cfg.MinioSecretKey,
		UseSSL:    cfg.MinioUseSSL,
		Bucket:    cfg.MinioBucket,
		PublicURL: cfg.MinioPublicURL,
	})
	if err != nil {
		logger.Error("MinIO unavailable, continuing without object storage", zap.Error(err))
		objectStore = nil
	}
	if objectStore.Enabled() {
		logger.Info("Connected to MinIO", zap.String("bucket", cfg.MinioBucket))
	}

	// 3. RabbitMQ
	rmqClient, err := rabbitmq.NewClient(cfg.RabbitMQURL, logger.Log)
	if err != nil {
		logger.Error("Failed to connect to RabbitMQ", zap.Error(err))
		os.Exit(1)
	}

	// 4. WhatsApp manager
	dispatcher := whatsapp.NewEventDispatcher()
	manager, err := whatsapp.NewMultiClientManager(whatsapp.Options{
		DB:         db,
		Dialect:    cfg.DBDialect,
		WALogLevel: cfg.WALogLevel,
		Debug:      cfg.Debug,
	}, dispatcher, rmqClient)
	if err != nil {
		logger.Error("Failed to initialize manager", zap.Error(err))
		os.Exit(1)
	}

	if err := manager.Start(ctx); err != nil {
		logger.Error("Failed to start WhatsApp manager", zap.Error(err))
		os.Exit(1)
	}

	// 5. Queue workers (after the manager so every instance already has a client)
	msgWorker := messaging.NewWorker(rmqClient, manager, logger.Log)
	msgWorker.Start()

	mediaWorker := messaging.NewMediaWorker(rmqClient, manager, logger.Log)
	mediaWorker.Start()

	whWorker := webhook.NewWorker(rmqClient, logger.Log, cfg.WebhookURL)
	whWorker.Start()

	// 6. HTTP API
	handler := api.NewHandler(api.Deps{
		Manager:  manager,
		RabbitMQ: rmqClient,
		DB:       db,
		Redis:    redisClient,
		Storage:  objectStore,
		Config:   cfg,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: QR codes and events are streamed over SSE/WebSocket.
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("Shutdown signal received")
	case err := <-serverErr:
		logger.Error("Server failed", zap.Error(err))
	}

	// Graceful shutdown: stop taking requests, drain queue handlers, then close connections.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	logger.Info("Shutting down HTTP server...")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown error", zap.Error(err))
	}

	logger.Info("Draining queue consumers...")
	rmqClient.StopConsumers(cfg.ShutdownTimeout / 2)

	logger.Info("Disconnecting WhatsApp clients...")
	manager.Shutdown()

	rmqClient.Close()
	logger.Info("Shutdown complete")
}
