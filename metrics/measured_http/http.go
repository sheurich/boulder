package measured_http

import (
	"fmt"
	"net/http"

	"github.com/jmhodges/clock"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	responseTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "response_time",
			Help: "Time taken to respond to a request",
		},
		[]string{"endpoint", "method", "code"})
)

func init() {
	prometheus.MustRegister(responseTime)
}

// responseWriterWithStatus satisfies http.ResponseWriter, but keeps track of the
// status code for gathering stats.
type responseWriterWithStatus struct {
	http.ResponseWriter
	code int
}

// WriteHeader stores a status code for generating stats.
func (r *responseWriterWithStatus) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// MeasuredHandler wraps an http.Handler and records prometheus stats
type MeasuredHandler struct {
	*http.ServeMux
	clk clock.Clock
	// Normally this is always responseTime, but we override it for testing.
	stat *prometheus.HistogramVec
}

func New(m *http.ServeMux, clk clock.Clock) *MeasuredHandler {
	return &MeasuredHandler{
		ServeMux: m,
		clk:      clk,
		stat:     responseTime,
	}
}

func (h *MeasuredHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	begin := h.clk.Now()
	rwws := &responseWriterWithStatus{w, 0}

	subHandler, pattern := h.Handler(r)
	defer func() {
		h.stat.With(prometheus.Labels{
			"endpoint": pattern,
			"method":   r.Method,
			"code":     fmt.Sprintf("%d", rwws.code),
		}).Observe(h.clk.Since(begin).Seconds())
	}()

	subHandler.ServeHTTP(rwws, r)
}
