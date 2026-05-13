package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

type Client struct {
	url      string
	conn     *amqp.Connection
	ch       *amqp.Channel
	logger   *zap.Logger
	mu       sync.Mutex
	closed   bool
	isReady  bool
}

func NewClient(url string, logger *zap.Logger) (*Client, error) {
	client := &Client{
		url:    url,
		logger: logger,
	}

	err := client.Connect()
	if err != nil {
		return nil, err
	}

	go client.handleReconnect()
	return client, nil
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open a channel: %w", err)
	}

	c.conn = conn
	c.ch = ch
	c.closed = false
	c.isReady = true

	c.logger.Info("Connected to RabbitMQ successfully")

	// Declare queues
	_, err = c.ch.QueueDeclare("send_message_queue", true, false, false, false, nil)
	if err != nil {
		return err
	}
	_, err = c.ch.QueueDeclare("webhook_events_queue", true, false, false, false, nil)
	if err != nil {
		return err
	}
	_, err = c.ch.QueueDeclare("send_media_queue", true, false, false, false, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) handleReconnect() {
	for {
		c.mu.Lock()
		isClosed := c.closed
		c.mu.Unlock()

		if isClosed {
			return
		}

		c.mu.Lock()
		isReady := c.isReady
		c.mu.Unlock()

		if !isReady {
			c.logger.Info("Attempting to reconnect to RabbitMQ...")
			err := c.Connect()
			if err != nil {
				c.logger.Error("Failed to reconnect, retrying in 5s", zap.Error(err))
				time.Sleep(5 * time.Second)
				continue
			}
		}

		// Watch connection closes
		c.mu.Lock()
		connCloseChan := make(chan *amqp.Error)
		if c.conn != nil {
			c.conn.NotifyClose(connCloseChan)
		}
		c.mu.Unlock()

		select {
		case err := <-connCloseChan:
			if err != nil {
				c.logger.Error("RabbitMQ connection closed", zap.Error(err))
			}
			c.mu.Lock()
			c.isReady = false
			c.mu.Unlock()
		}
	}
}

func (c *Client) Publish(ctx context.Context, queueName string, body []byte) error {
	c.mu.Lock()
	if !c.isReady || c.ch == nil {
		c.mu.Unlock()
		return fmt.Errorf("RabbitMQ is not connected")
	}
	ch := c.ch
	c.mu.Unlock()

	err := ch.PublishWithContext(ctx,
		"",        // exchange
		queueName, // routing key
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
		})
	
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}
	return nil
}

func (c *Client) Consume(queueName string, handler func([]byte) error) error {
	c.mu.Lock()
	if !c.isReady || c.ch == nil {
		c.mu.Unlock()
		return fmt.Errorf("RabbitMQ is not connected")
	}
	ch := c.ch
	c.mu.Unlock()

	msgs, err := ch.Consume(
		queueName,
		"",    // consumer
		false, // auto-ack (we'll manually ack)
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("failed to register a consumer: %w", err)
	}

	go func() {
		for d := range msgs {
			err := handler(d.Body)
			if err != nil {
				c.logger.Error("Failed to process message, requeuing", zap.Error(err))
				d.Nack(false, true) // Requeue
			} else {
				d.Ack(false)
			}
		}
	}()

	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.isReady = false
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
