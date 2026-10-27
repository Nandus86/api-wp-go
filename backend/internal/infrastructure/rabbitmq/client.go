package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// Queues used by the application. The main queues keep their original
// declaration (durable, no arguments) so existing brokers stay compatible.
const (
	QueueSendMessage     = "send_message_queue"
	QueueSendMedia       = "send_media_queue"
	QueueWebhookEvents   = "webhook_events_queue"
	QueueWebhookDelivery = "webhook_delivery_queue"
)

var mainQueues = []string{QueueSendMessage, QueueSendMedia, QueueWebhookEvents, QueueWebhookDelivery}

const (
	// A failed delivery waits retryDelay in "<queue>.retry" and is then dead-lettered
	// back to "<queue>". After maxRedeliveries it is parked in "<queue>.dead".
	retryDelay      = 10 * time.Second
	maxRedeliveries = 10
	retryHeader     = "x-retry-count"
	errorHeader     = "x-last-error"
)

type Handler func(body []byte) error

type consumer struct {
	queue       string
	concurrency int
	handler     Handler
	tag         string
	ch          *amqp.Channel
}

type Client struct {
	url    string
	logger *zap.Logger

	mu        sync.Mutex
	conn      *amqp.Connection
	pubCh     *amqp.Channel
	closed    bool
	isReady   bool
	consumers []*consumer
	stopping  bool

	inflight sync.WaitGroup
}

func NewClient(url string, logger *zap.Logger) (*Client, error) {
	client := &Client{url: url, logger: logger}

	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = client.Connect(); err == nil {
			go client.handleReconnect()
			return client, nil
		}
		logger.Warn("RabbitMQ not reachable yet, retrying", zap.Error(err), zap.Int("attempt", attempt+1))
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return nil, err
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := amqp.DialConfig(c.url, amqp.Config{Heartbeat: 10 * time.Second, Locale: "en_US"})
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open a channel: %w", err)
	}
	if err := declareTopology(ch); err != nil {
		conn.Close()
		return err
	}
	if err := ch.Confirm(false); err != nil {
		conn.Close()
		return fmt.Errorf("failed to enable publisher confirms: %w", err)
	}

	c.conn = conn
	c.pubCh = ch
	c.closed = false
	c.isReady = true

	c.logger.Info("Connected to RabbitMQ successfully")

	// (Re)start every registered consumer on the fresh connection.
	for _, cons := range c.consumers {
		if err := c.startConsumerLocked(cons); err != nil {
			c.logger.Error("Failed to start consumer", zap.String("queue", cons.queue), zap.Error(err))
		}
	}
	return nil
}

func declareTopology(ch *amqp.Channel) error {
	for _, q := range mainQueues {
		if _, err := ch.QueueDeclare(q, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare %s: %w", q, err)
		}
		if _, err := ch.QueueDeclare(q+".retry", true, false, false, false, amqp.Table{
			"x-message-ttl":             int32(retryDelay / time.Millisecond),
			"x-dead-letter-exchange":    "",
			"x-dead-letter-routing-key": q,
		}); err != nil {
			return fmt.Errorf("declare %s.retry: %w", q, err)
		}
		if _, err := ch.QueueDeclare(q+".dead", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare %s.dead: %w", q, err)
		}
	}
	return nil
}

func (c *Client) handleReconnect() {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		conn := c.conn
		c.mu.Unlock()

		if conn != nil {
			closeChan := conn.NotifyClose(make(chan *amqp.Error, 1))
			err, ok := <-closeChan
			if ok && err != nil {
				c.logger.Error("RabbitMQ connection closed", zap.Error(err))
			}
		}

		c.mu.Lock()
		c.isReady = false
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}

		for backoff := time.Second; ; {
			c.logger.Info("Attempting to reconnect to RabbitMQ...")
			if err := c.Connect(); err == nil {
				break
			} else {
				c.logger.Error("Failed to reconnect to RabbitMQ", zap.Error(err), zap.Duration("retry_in", backoff))
			}
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if closed {
				return
			}
		}
	}
}

// IsReady reports whether the broker connection is currently usable.
func (c *Client) IsReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isReady && c.conn != nil && !c.conn.IsClosed()
}

func (c *Client) Publish(ctx context.Context, queueName string, body []byte) error {
	return c.PublishWithHeaders(ctx, queueName, body, nil)
}

// PublishWithHeaders publishes a persistent message and waits for the broker confirm,
// so a nil error means the message is safely stored by RabbitMQ.
func (c *Client) PublishWithHeaders(ctx context.Context, queueName string, body []byte, headers amqp.Table) error {
	c.mu.Lock()
	if !c.isReady || c.pubCh == nil {
		c.mu.Unlock()
		return fmt.Errorf("RabbitMQ is not connected")
	}
	ch := c.pubCh
	c.mu.Unlock()

	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	confirm, err := ch.PublishWithDeferredConfirmWithContext(pctx,
		"",        // exchange
		queueName, // routing key
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Headers:      headers,
			Timestamp:    time.Now(),
			Body:         body,
		})
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}
	if confirm == nil {
		return nil
	}
	acked, err := confirm.WaitContext(pctx)
	if err != nil {
		return fmt.Errorf("publish confirm: %w", err)
	}
	if !acked {
		return fmt.Errorf("broker rejected message for %s", queueName)
	}
	return nil
}

// Consume registers a consumer with a single worker. Kept for backwards compatibility.
func (c *Client) Consume(queueName string, handler func([]byte) error) error {
	return c.ConsumeN(queueName, 1, handler)
}

// ConsumeN registers a consumer processed by `concurrency` workers. The registration
// survives reconnects: the consumer is restarted every time the connection comes back.
// A handler error sends the message to the retry queue (10s delay); after
// maxRedeliveries attempts it is parked in the dead-letter queue.
func (c *Client) ConsumeN(queueName string, concurrency int, handler func([]byte) error) error {
	if concurrency < 1 {
		concurrency = 1
	}
	cons := &consumer{queue: queueName, concurrency: concurrency, handler: handler}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.consumers = append(c.consumers, cons)
	if !c.isReady || c.conn == nil {
		// Will be started by Connect() once the broker is reachable.
		return nil
	}
	return c.startConsumerLocked(cons)
}

func (c *Client) startConsumerLocked(cons *consumer) error {
	if c.stopping {
		return nil
	}
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("open consumer channel: %w", err)
	}
	if err := ch.Qos(cons.concurrency*2, 0, false); err != nil {
		ch.Close()
		return fmt.Errorf("set qos: %w", err)
	}
	cons.tag = fmt.Sprintf("%s-%d", cons.queue, time.Now().UnixNano())
	msgs, err := ch.Consume(cons.queue, cons.tag, false, false, false, false, nil)
	if err != nil {
		ch.Close()
		return fmt.Errorf("failed to register a consumer: %w", err)
	}
	cons.ch = ch

	for i := 0; i < cons.concurrency; i++ {
		go func() {
			for d := range msgs {
				c.inflight.Add(1)
				c.process(cons, d)
				c.inflight.Done()
			}
		}()
	}
	return nil
}

func (c *Client) process(cons *consumer, d amqp.Delivery) {
	err := safeCall(cons.handler, d.Body)
	if err == nil {
		d.Ack(false)
		return
	}

	retries := headerInt(d.Headers, retryHeader)
	target := cons.queue + ".retry"
	if retries+1 > maxRedeliveries {
		target = cons.queue + ".dead"
	}
	headers := amqp.Table{retryHeader: int32(retries + 1), errorHeader: truncate(err.Error(), 512)}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pubErr := c.PublishWithHeaders(ctx, target, d.Body, headers)
	cancel()
	if pubErr != nil {
		// Could not park it anywhere: give it back to the broker, but slow down first so
		// a persistent failure does not turn into a hot loop.
		c.logger.Error("Failed to route failed message, requeuing", zap.String("queue", cons.queue), zap.Error(pubErr))
		time.Sleep(2 * time.Second)
		d.Nack(false, true)
		return
	}
	if target == cons.queue+".dead" {
		c.logger.Error("Message moved to dead-letter queue", zap.String("queue", cons.queue), zap.Int("attempts", retries+1), zap.Error(err))
	} else {
		c.logger.Warn("Message processing failed, scheduled for retry", zap.String("queue", cons.queue), zap.Int("attempt", retries+1), zap.Error(err))
	}
	d.Ack(false)
}

func safeCall(h Handler, body []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in consumer: %v", r)
		}
	}()
	return h(body)
}

func headerInt(h amqp.Table, key string) int {
	switch v := h[key].(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// QueueDepth returns the number of ready messages in a queue (used by metrics/health).
func (c *Client) QueueDepth(queue string) (int, error) {
	c.mu.Lock()
	conn := c.conn
	ready := c.isReady
	c.mu.Unlock()
	if !ready || conn == nil {
		return 0, fmt.Errorf("RabbitMQ is not connected")
	}
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	defer ch.Close()
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		return 0, err
	}
	return q.Messages, nil
}

// StopConsumers cancels every consumer and waits (up to timeout) for in-flight handlers.
func (c *Client) StopConsumers(timeout time.Duration) {
	c.mu.Lock()
	c.stopping = true
	for _, cons := range c.consumers {
		if cons.ch != nil {
			cons.ch.Cancel(cons.tag, false)
		}
	}
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		c.logger.Warn("Timed out waiting for in-flight queue handlers")
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.isReady = false
	for _, cons := range c.consumers {
		if cons.ch != nil {
			cons.ch.Close()
		}
	}
	if c.pubCh != nil {
		c.pubCh.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
