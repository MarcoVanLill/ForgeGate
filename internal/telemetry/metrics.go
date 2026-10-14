package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry         *prometheus.Registry
	requests         *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	providerCalls    *prometheus.CounterVec
	providerDuration *prometheus.HistogramVec
	fallbacks        *prometheus.CounterVec
	rateLimited      prometheus.Counter
	circuit          *prometheus.GaugeVec
	jobs             *prometheus.CounterVec
	jobDuration      *prometheus.HistogramVec
	queueDepth       prometheus.Gauge
}

func NewMetrics() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry(), requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "forgegate_http_requests_total", Help: "HTTP requests by stable route, method, and status."}, []string{"route", "method", "status"}), requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "forgegate_http_request_duration_seconds", Help: "HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"route", "method"}), providerCalls: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "forgegate_provider_calls_total", Help: "Provider calls by provider and outcome."}, []string{"provider", "outcome"}), providerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "forgegate_provider_duration_seconds", Help: "Provider call duration.", Buckets: prometheus.DefBuckets}, []string{"provider"}), fallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "forgegate_provider_fallbacks_total", Help: "Provider fallback transitions."}, []string{"from", "to"}), rateLimited: prometheus.NewCounter(prometheus.CounterOpts{Name: "forgegate_rate_limit_rejections_total", Help: "Requests rejected by tenant rate limiting."}), circuit: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "forgegate_provider_circuit_open", Help: "Whether a provider circuit is open."}, []string{"provider"}), jobs: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "forgegate_jobs_total", Help: "Jobs processed by outcome."}, []string{"outcome"}), jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "forgegate_job_processing_duration_seconds", Help: "Job processing duration.", Buckets: prometheus.DefBuckets}, []string{"outcome"}), queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{Name: "forgegate_job_queue_depth", Help: "Observed JetStream pending job count."})}
	m.registry.MustRegister(m.requests, m.requestDuration, m.providerCalls, m.providerDuration, m.fallbacks, m.rateLimited, m.circuit, m.jobs, m.jobDuration, m.queueDepth)
	return m
}
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
func (m *Metrics) ObserveHTTP(route, method string, status int, d time.Duration) {
	m.requests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.requestDuration.WithLabelValues(route, method).Observe(d.Seconds())
}
func (m *Metrics) ProviderCall(provider, outcome string, d time.Duration) {
	m.providerCalls.WithLabelValues(provider, outcome).Inc()
	m.providerDuration.WithLabelValues(provider).Observe(d.Seconds())
}
func (m *Metrics) ProviderFallback(from, to string) { m.fallbacks.WithLabelValues(from, to).Inc() }
func (m *Metrics) CircuitState(provider, state string) {
	value := 0.0
	if state == "open" {
		value = 1
	}
	m.circuit.WithLabelValues(provider).Set(value)
}
func (m *Metrics) RateLimited() { m.rateLimited.Inc() }
func (m *Metrics) Job(outcome string, d time.Duration) {
	m.jobs.WithLabelValues(outcome).Inc()
	m.jobDuration.WithLabelValues(outcome).Observe(d.Seconds())
}
func (m *Metrics) QueueDepth(depth float64) { m.queueDepth.Set(depth) }
