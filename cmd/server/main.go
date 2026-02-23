package main

import (
    "net/http"
	"os"
	"os/signal"
	"syscall"

    "github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
    "github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
    "github.com/user/whatsmeow-basileia/internal/service/messaging"
    "github.com/user/whatsmeow-basileia/internal/service/webhook"
    "github.com/user/whatsmeow-basileia/internal/api"
    "github.com/user/whatsmeow-basileia/pkg/config"
    "github.com/user/whatsmeow-basileia/pkg/logger"
    "go.uber.org/zap"
    _ "github.com/lib/pq"
)

// @title WhatsMeow Basileia API
// @version 1.0
// @description API for WhatsApp automation using WhatsMeow.
// @BasePath /api

func main() {
    logger.InitLogger()
    cfg := config.Load()
    
    logger.Info("Starting WhatsMeow Basileia Service...", zap.String("port", cfg.HTTPPort))

    // 1. Initialize Dispatcher
    dispatcher := whatsapp.NewEventDispatcher()
    
    // 2. Initialize RabbitMQ Client & Workers
    rmqClient, err := rabbitmq.NewClient(cfg.RabbitMQURL, logger.Log)
    if err != nil {
        logger.Error("Failed to connect to RabbitMQ", zap.Error(err))
        os.Exit(1)
    }
    defer rmqClient.Close()

    // 3. Initialize Manager (now with rmqClient for webhook dispatch)
    manager, err := whatsapp.NewMultiClientManager(cfg.DBDialect, cfg.DBAddress, dispatcher, rmqClient)
    if err != nil {
        logger.Error("Failed to initialize manager", zap.Error(err))
        os.Exit(1)
    }

    msgWorker := messaging.NewWorker(rmqClient, manager, logger.Log)
    msgWorker.Start()

    mediaWorker := messaging.NewMediaWorker(rmqClient, manager, logger.Log)
    mediaWorker.Start()

    whWorker := webhook.NewWorker(rmqClient, logger.Log, cfg.WebhookURL)
    whWorker.Start()

    // 3. Initialize API
    handler := api.NewHandler(manager, rmqClient)
    router := handler.Router()

    // 4. Start Server
    srv := &http.Server{
        Addr:    ":" + cfg.HTTPPort,
        Handler: router,
    }

    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            logger.Error("Server failed", zap.Error(err))
        }
    }()

    // Wait for interrupt signal
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    <-c

    logger.Info("Shutting down...")
    // manager.Close() 
}


