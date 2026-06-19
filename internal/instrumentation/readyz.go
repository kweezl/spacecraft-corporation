package instrumentation

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// readinessTimeout bounds how long the readiness probes may run per request, so
// a hung dependency (a stalled DB ping) can't pin the endpoint open.
const readinessTimeout = 3 * time.Second

// readyzHandler serves readiness: 200 only when every readiness check passes,
// 503 ("starting") otherwise. The orchestrator polls this to decide whether to
// route traffic, so it must reflect live dependency health on every request.
func readyzHandler(ready *Readiness, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), readinessTimeout)
		defer cancel()
		if err := ready.Check(ctx); err != nil {
			log.Debug("not ready", zap.Error(err))
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("starting"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
