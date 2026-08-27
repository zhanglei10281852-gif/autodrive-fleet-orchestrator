package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type retainedBodyWriter struct {
	header  http.Header
	status  int
	entered chan struct{}
	release chan struct{}
	body    []byte
}

func newRetainedBodyWriter() *retainedBodyWriter {
	return &retainedBodyWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
}

func (w *retainedBodyWriter) Header() http.Header { return w.header }

func (w *retainedBodyWriter) WriteHeader(status int) { w.status = status }

func (w *retainedBodyWriter) Write(body []byte) (int, error) {
	close(w.entered)
	<-w.release
	w.body = append(w.body, body...)
	return len(body), nil
}

func TestConcurrentJSONResponsesRemainRequestScoped(t *testing.T) {
	firstPayload := map[string]string{"request_id": "request-alpha", "vehicle": strings.Repeat("A", 256)}
	secondPayload := map[string]string{"request_id": "request-bravo", "vehicle": strings.Repeat("B", 256)}
	firstHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, firstPayload)
	})
	secondHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusAccepted, secondPayload)
	})

	retained := newRetainedBodyWriter()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		firstHandler.ServeHTTP(retained, httptest.NewRequest(http.MethodGet, "/v1/vehicles/alpha", nil))
	}()
	<-retained.entered

	second := httptest.NewRecorder()
	secondHandler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/v1/vehicles/bravo", nil))
	close(retained.release)
	<-firstDone

	var firstBody, secondBody map[string]string
	if err := json.Unmarshal(retained.body, &firstBody); err != nil {
		t.Fatalf("decode first response: %v body=%q", err, retained.body)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second response: %v body=%q", err, second.Body.Bytes())
	}
	if retained.status != http.StatusOK || firstBody["request_id"] != "request-alpha" || firstBody["vehicle"] != firstPayload["vehicle"] {
		t.Fatalf("first response was contaminated: status=%d body=%v", retained.status, firstBody)
	}
	if second.Code != http.StatusAccepted || secondBody["request_id"] != "request-bravo" || secondBody["vehicle"] != secondPayload["vehicle"] {
		t.Fatalf("second response changed: status=%d body=%v", second.Code, secondBody)
	}

	sequential := httptest.NewRecorder()
	firstHandler.ServeHTTP(sequential, httptest.NewRequest(http.MethodGet, "/v1/vehicles/alpha", nil))
	var sequentialBody map[string]string
	if err := json.Unmarshal(sequential.Body.Bytes(), &sequentialBody); err != nil {
		t.Fatalf("decode sequential response: %v", err)
	}
	if sequential.Code != http.StatusOK || sequentialBody["request_id"] != "request-alpha" {
		t.Fatalf("sequential response changed: status=%d body=%v", sequential.Code, sequentialBody)
	}
}
