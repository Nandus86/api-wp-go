package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"go.uber.org/zap"
)

type Worker struct {
	rmqClient  *rabbitmq.Client
	logger     *zap.Logger
	webhookURL string
	httpClient *http.Client
}

func NewWorker(rmqClient *rabbitmq.Client, logger *zap.Logger, webhookURL string) *Worker {
	return &Worker{
		rmqClient:  rmqClient,
		logger:     logger,
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (w *Worker) Start() {
	err := w.rmqClient.Consume("webhook_events_queue", w.handleWebhookEvent)
	if err != nil {
		w.logger.Error("Failed to start consuming webhook_events_queue", zap.Error(err))
	} else {
		w.logger.Info("Started RabbitMQ worker for webhook_events_queue", zap.String("global_url", w.webhookURL))
	}
}

func (w *Worker) handleWebhookEvent(body []byte) error {
	var payload struct {
		WebhookURL string `json:"webhook_url"`
	}
	json.Unmarshal(body, &payload)

	targetURL := payload.WebhookURL
	if targetURL == "" {
		targetURL = w.webhookURL
	}

	if targetURL == "" {
		// No webhook URL configured (neither instance nor global), drop message
		return nil
	}

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewBuffer(body))
		if err != nil {
			w.logger.Error("Failed to create webhook request", zap.Error(err))
			return nil // Drop message
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := w.httpClient.Do(req)
		if err != nil {
			w.logger.Error("Failed to send webhook, will retry", zap.Error(err), zap.Int("attempt", i+1))
		} else {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				w.logger.Info("Webhook dispatched successfully", zap.Int("status", resp.StatusCode))
				return nil
			}
			w.logger.Warn("Webhook received non-2xx response, will retry", zap.Int("status", resp.StatusCode), zap.Int("attempt", i+1))
		}

		if i < maxRetries-1 {
			time.Sleep(time.Duration(i+1) * 2 * time.Second) // backoff: 2s, 4s
		}
	}

	w.logger.Error("Webhook failed after max retries, dropping message")
	return nil
}
