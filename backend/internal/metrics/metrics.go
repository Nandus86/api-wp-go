// Package metrics exposes Prometheus metrics for the API, queues, webhooks and sessions.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	reg *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	sends        *prometheus.CounterVec
	inbound      prometheus.Counter
	webhooks     *prometheus.CounterVec
	webhookDur   prometheus.Histogram
}

// Sources are read at scrape time.
type Sources struct {
	Instances   func() (connected, total int)
	QueueDepth  func(queue string) (int, error)
	Queues      []string
	Subscribers func() int
}

func New(src Sources) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &Metrics{
		reg: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wmb_http_requests_total", Help: "HTTP requests by route and status.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wmb_http_request_duration_seconds", Help: "HTTP request latency.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		sends: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wmb_messages_sent_total", Help: "Outbound sends by kind (message, media) and outcome (sent, retry, failed, deferred).",
		}, []string{"kind", "outcome"}),
		inbound: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wmb_messages_received_total", Help: "Inbound WhatsApp messages.",
		}),
		webhooks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wmb_webhook_deliveries_total", Help: "Webhook delivery attempts by event and outcome (delivered, retry, failed).",
		}, []string{"event", "outcome"}),
		webhookDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "wmb_webhook_delivery_duration_seconds", Help: "Webhook HTTP round-trip time.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 15},
		}),
	}
	reg.MustRegister(m.httpRequests, m.httpDuration, m.sends, m.inbound, m.webhooks, m.webhookDur)

	if src.Instances != nil {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "wmb_instances_connected", Help: "Instances with a live, logged-in WhatsApp connection.",
		}, func() float64 { c, _ := src.Instances(); return float64(c) }))
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "wmb_instances_total", Help: "Instances registered.",
		}, func() float64 { _, t := src.Instances(); return float64(t) }))
	}
	if src.QueueDepth != nil {
		for _, q := range src.Queues {
			q := q
			reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "wmb_queue_depth", Help: "Ready messages in a RabbitMQ queue.", ConstLabels: prometheus.Labels{"queue": q},
			}, func() float64 {
				n, err := src.QueueDepth(q)
				if err != nil {
					return -1
				}
				return float64(n)
			}))
		}
	}
	if src.Subscribers != nil {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "wmb_realtime_subscribers", Help: "Connected WebSocket/SSE subscribers.",
		}, func() float64 { return float64(src.Subscribers()) }))
	}
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveSend(kind, outcome string) { m.sends.WithLabelValues(kind, outcome).Inc() }
func (m *Metrics) IncInbound()                      { m.inbound.Inc() }

func (m *Metrics) ObserveWebhook(event, outcome string, d time.Duration) {
	m.webhooks.WithLabelValues(event, outcome).Inc()
	m.webhookDur.Observe(d.Seconds())
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(c int) {
	if r.status == 0 {
		r.status = c
	}
	r.ResponseWriter.WriteHeader(c)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Middleware records request count and latency labelled by chi route pattern
// (never the raw path, which would explode cardinality with ids).
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		m.httpRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		m.httpDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
