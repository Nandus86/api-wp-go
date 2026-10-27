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
	"github.com/user/whatsmeow-basileia/internal/audit"
	"github.com/user/whatsmeow-basileia/internal/auth"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/database"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	redisinfra "github.com/user/whatsmeow-basileia/internal/infrastructure/redis"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/storage"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/internal/metrics"
	"github.com/user/whatsmeow-basileia/internal/realtime"
	"github.com/user/whatsmeow-basileia/internal/service/messages"
	"github.com/user/whatsmeow-basileia/internal/service/messaging"
	"github.com/user/whatsmeow-basileia/internal/service/webhook"
	"github.com/user/whatsmeow-basileia/pkg/config"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
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

	// Webhooks: transactional outbox (RabbitMQ buffer -> Postgres -> signed HTTP delivery)
	webhookService := webhook.NewService(webhook.NewStore(db), rmqClient, webhook.Config{
		GlobalURL:     cfg.WebhookURL,
		Secret:        cfg.WebhookSecret,
		Timeout:       cfg.WebhookTimeout,
		MaxAttempts:   cfg.WebhookMaxAttempts,
		Workers:       cfg.WebhookWorkers,
		RetentionDays: cfg.WebhookRetentionDays,
		UserAgent:     "whatsmeow-basileia/" + api.Version,
	})
	// Message history + outbound jobs (Postgres) with send pacing (Redis)
	messaging.Debug = cfg.Debug
	history := messages.NewStore(db, objectStore, messages.Options{
		ArchiveMedia:  cfg.MediaArchiveEnabled,
		MediaMaxBytes: cfg.MediaMaxBytes,
	})
	pacer := messaging.NewPacer(messaging.PacingConfig{
		Enabled:     cfg.SendPacingEnabled,
		Schedule:    cfg.SendPacingSchedule,
		ColdCap:     cfg.SendPacingColdCap,
		FailBreaker: cfg.SendPacingFailBreaker,
	}, redisClient)
	messagingService := messaging.NewService(messaging.NewJobStore(db), rmqClient, manager, history, pacer, objectStore, messaging.Config{
		Workers:        cfg.MessageWorkers,
		MaxAttempts:    cfg.MessageMaxAttempts,
		SimulateTyping: cfg.SimulateTyping,
		RetentionDays:  cfg.MessageRetentionDays,
		MediaMaxBytes:  cfg.MediaMaxBytes,
		ArchiveMedia:   cfg.MediaArchiveEnabled,
	}, logger.Log)

	// Realtime fan-out (WebSocket/SSE, across replicas through Redis) and metrics
	hub := realtime.NewHub(redisClient)
	hub.Start(ctx)
	mtr := metrics.New(metrics.Sources{
		Instances:   manager.ConnectedCount,
		QueueDepth:  rmqClient.QueueDepth,
		Queues:      []string{rabbitmq.QueueSendMessage, rabbitmq.QueueSendMedia, rabbitmq.QueueWebhookEvents, rabbitmq.QueueWebhookDelivery},
		Subscribers: hub.Subscribers,
	})
	messagingService.SetObserver(mtr.ObserveSend)
	webhookService.SetObserver(mtr.ObserveWebhook)

	manager.SetHooks(whatsapp.Hooks{
		Emit: func(ev whatsapp.OutboundEvent) {
			webhookService.Emit(ev)
			hub.Publish(ev)
		},
		OnMessage: func(instanceID string, client *whatsmeow.Client, evt *events.Message) {
			if !evt.Info.IsFromMe {
				mtr.IncInbound()
			}
			history.SaveIncoming(instanceID, client, evt)
		},
		OnEvent: func(instanceID string, evt interface{}) {
			switch evt.(type) {
			case *events.LabelEdit, *events.LabelAssociationChat:
				go history.HandleAppStateEvent(instanceID, evt)
			}
		},
		OnReceipt: func(instanceID string, evt *events.Receipt) {
			status := messages.StatusFromReceipt(evt.Type)
			if status == "" {
				return
			}
			ids := make([]string, len(evt.MessageIDs))
			for i, id := range evt.MessageIDs {
				ids[i] = string(id)
			}
			go func() {
				rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = history.UpdateStatus(rctx, instanceID, ids, status)
			}()
		},
	})

	if err := manager.Start(ctx); err != nil {
		logger.Error("Failed to start WhatsApp manager", zap.Error(err))
		os.Exit(1)
	}

	// 5. Queue workers (after the manager so every instance already has a client)
	messagingService.Start(ctx)
	webhookService.Start(ctx)

	// 6. Authentication + audit
	authService := auth.NewService(db, cfg.AdminAPIKey, func(token string) (string, string, bool) {
		inst, ok := manager.InstanceByAPIKey(token)
		if !ok {
			return "", "", false
		}
		return inst.ID, inst.Name, true
	})
	if cfg.AuthRequired {
		key, path, err := authService.Bootstrap(ctx, cfg.DataDir)
		if err != nil {
			logger.Error("Failed to bootstrap admin API key", zap.Error(err))
			os.Exit(1)
		}
		if key != "" {
			// Printed once, like OpenWA: later boots only find the hash in the database.
			logger.Log.Warn("Generated the first admin API key. Store it now, it will not be shown again.",
				zap.String("admin_api_key", key), zap.String("saved_to", path))
		}
	} else {
		logger.Warn("AUTH_REQUIRED=false: the API is open to anyone who can reach it (legacy mode)")
	}
	auditStore := audit.NewStore(db)

	// 7. HTTP API
	handler, err := api.NewHandler(api.Deps{
		Manager:   manager,
		RabbitMQ:  rmqClient,
		DB:        db,
		Redis:     redisClient,
		Storage:   objectStore,
		Config:    cfg,
		Auth:      authService,
		Audit:     auditStore,
		Webhooks:  webhookService,
		Messaging: messagingService,
		Hub:       hub,
		Metrics:   mtr,
	})
	if err != nil {
		logger.Error("Invalid HTTP configuration", zap.Error(err))
		os.Exit(1)
	}

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
