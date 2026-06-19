package instrumentation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// check is a test helper that builds a named readiness probe returning err.
func check(name string, err error) ReadinessCheck {
	return ReadinessCheck{Name: name, Probe: func(context.Context) error { return err }}
}

func TestHealthz_AlwaysOK(t *testing.T) {
	rec := get(t, healthzHandler(), "/healthz")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReadyz_503UntilAllChecksPass(t *testing.T) {
	// One failing check keeps readiness red even when another passes.
	failing := &Readiness{checks: []ReadinessCheck{
		check("postgres", nil),
		check("discord", errors.New("gateway not connected")),
	}}
	assert.Equal(t, http.StatusServiceUnavailable,
		get(t, readyzHandler(failing, zap.NewNop()), "/readyz").Code)

	// All checks passing flips it green.
	ready := &Readiness{checks: []ReadinessCheck{
		check("postgres", nil),
		check("discord", nil),
	}}
	assert.Equal(t, http.StatusOK,
		get(t, readyzHandler(ready, zap.NewNop()), "/readyz").Code)
}

func TestReadyz_NoChecksIsReady(t *testing.T) {
	// With no contributed checks the app is trivially ready (vacuous truth).
	rec := get(t, readyzHandler(&Readiness{}, zap.NewNop()), "/readyz")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReadiness_Check_ReportsFailingName(t *testing.T) {
	r := &Readiness{checks: []ReadinessCheck{
		check("postgres", nil),
		check("discord", errors.New("boom")),
	}}
	err := r.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discord", "failure should be prefixed with the check name")
}

func TestMetrics_Served(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_total", Help: "x"})
	reg.MustRegister(c)
	c.Inc()

	rec := get(t, metricsHandler(reg), "/metrics")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "test_total")
}
