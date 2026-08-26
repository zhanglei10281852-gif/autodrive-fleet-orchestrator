package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
)

type observedCorrelation struct {
	path string
	id   string
}

func TestOverlappingRequestsKeepIndependentCorrelationIDs(t *testing.T) {
	alphaEntered := make(chan struct{})
	releaseAlpha := make(chan struct{})
	observed := make(chan observedCorrelation, 3)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alpha" {
			close(alphaEntered)
			<-releaseAlpha
		}
		observed <- observedCorrelation{path: r.URL.Path, id: request.ID(r.Context())}
		w.WriteHeader(http.StatusNoContent)
	})
	wrapped := RequestID(idgen.NewSequence(1900))(handler)

	alphaRequest := httptest.NewRequest(http.MethodPost, "/alpha", nil)
	alphaRequest.Header.Set("X-Request-ID", "request-alpha")
	alphaResponse := httptest.NewRecorder()
	alphaDone := make(chan struct{})
	go func() {
		defer close(alphaDone)
		wrapped.ServeHTTP(alphaResponse, alphaRequest)
	}()
	<-alphaEntered

	bravoRequest := httptest.NewRequest(http.MethodPost, "/bravo", nil)
	bravoRequest.Header.Set("X-Request-ID", "request-bravo")
	bravoResponse := httptest.NewRecorder()
	wrapped.ServeHTTP(bravoResponse, bravoRequest)
	close(releaseAlpha)
	<-alphaDone

	idsByPath := map[string]string{}
	for range 2 {
		value := <-observed
		idsByPath[value.path] = value.id
	}
	if alphaResponse.Header().Get("X-Request-ID") != "request-alpha" || idsByPath["/alpha"] != "request-alpha" {
		t.Fatalf("alpha correlation crossed requests: header=%q context=%q", alphaResponse.Header().Get("X-Request-ID"), idsByPath["/alpha"])
	}
	if bravoResponse.Header().Get("X-Request-ID") != "request-bravo" || idsByPath["/bravo"] != "request-bravo" {
		t.Fatalf("bravo correlation changed: header=%q context=%q", bravoResponse.Header().Get("X-Request-ID"), idsByPath["/bravo"])
	}

	charlieRequest := httptest.NewRequest(http.MethodGet, "/charlie", nil)
	charlieRequest.Header.Set("X-Request-ID", "request-charlie")
	charlieResponse := httptest.NewRecorder()
	wrapped.ServeHTTP(charlieResponse, charlieRequest)
	charlie := <-observed
	if charlieResponse.Code != http.StatusNoContent || charlieResponse.Header().Get("X-Request-ID") != "request-charlie" || charlie.id != "request-charlie" {
		t.Fatalf("sequential correlation changed: status=%d header=%q observed=%+v", charlieResponse.Code, charlieResponse.Header().Get("X-Request-ID"), charlie)
	}
}
