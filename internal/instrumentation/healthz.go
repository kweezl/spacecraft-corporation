package instrumentation

import "net/http"

// healthzHandler serves liveness: always 200 once the server is listening. It
// must NOT depend on downstream health — that's readiness' job. A failing
// liveness probe makes the orchestrator restart the process, which can't fix a
// down dependency and only sheds a still-useful instance.
func healthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
