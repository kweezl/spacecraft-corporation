package health

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz_AlwaysOK(t *testing.T) {
	h := newHandler(newReadiness(), prometheus.NewRegistry())
	rec := get(t, h, "/healthz")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReadyz_NotReadyThenReady(t *testing.T) {
	ready := newReadiness()
	h := newHandler(ready, prometheus.NewRegistry())

	assert.Equal(t, http.StatusServiceUnavailable, get(t, h, "/readyz").Code)

	ready.SetReady()
	assert.Equal(t, http.StatusOK, get(t, h, "/readyz").Code)
}

func TestMetrics_Served(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_total", Help: "x"})
	reg.MustRegister(c)
	c.Inc()

	rec := get(t, newHandler(newReadiness(), reg), "/metrics")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "test_total")
}

// TestReadiness_FlipsAfterStart verifies the readiness flag is false before the
// app starts and true once MarkReady's OnStart hook has run.
func TestReadiness_FlipsAfterStart(t *testing.T) {
	t.Setenv("HEALTH_ADDR", "127.0.0.1:0") // ephemeral port, no conflicts

	var ready *Readiness
	app := fxtest.New(t,
		fx.Provide(func() *zap.Logger { return zap.NewNop() }),
		Module(),
		fx.Invoke(MarkReady),
		fx.Populate(&ready),
	)

	require.NotNil(t, ready)
	assert.False(t, ready.Ready(), "should not be ready before Start")

	app.RequireStart()
	defer app.RequireStop()

	assert.True(t, ready.Ready(), "should be ready after Start")
}
