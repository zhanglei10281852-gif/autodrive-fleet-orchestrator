package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestWriteJSONConcurrentResponsesAreIsolated guards against the fleet-dashboard bug
// where two concurrent detail refreshes crossed responses: the slow request received
// the fast request's body, or a half-overwritten body that failed to parse. The cause
// was a shared package-level buffer reused across goroutines; each request must now
// marshal into an isolated buffer and receive only its own complete object.
func TestWriteJSONConcurrentResponsesAreIsolated(t *testing.T) {
	type payload struct {
		Index  int    `json:"index"`
		Marker string `json:"marker"`
		Filler string `json:"filler"`
	}
	const goroutines = 48
	const rounds = 200
	// Distinct, sizable bodies widen the race window so a shared buffer reliably
	// corrupts or crosses bytes rather than slipping through undetected.
	fillers := make([]string, goroutines)
	for i := range fillers {
		buf := make([]byte, 4096)
		for j := range buf {
			buf[j] = byte('a' + (j+i)%26)
		}
		fillers[i] = string(buf)
	}
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		wg.Add(goroutines)
		start := make(chan struct{})
		for i := 0; i < goroutines; i++ {
			want := payload{Index: i, Marker: "fleet-detail", Filler: fillers[i]}
			go func(want payload) {
				defer wg.Done()
				<-start
				rec := httptest.NewRecorder()
				writeJSON(rec, http.StatusOK, want)
				if rec.Code != http.StatusOK {
					t.Errorf("index %d: status=%d want=%d", want.Index, rec.Code, http.StatusOK)
					return
				}
				if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
					t.Errorf("index %d: content-type=%q", want.Index, ct)
					return
				}
				var got payload
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Errorf("index %d: body did not parse as JSON: %v body=%q", want.Index, err, rec.Body.String())
					return
				}
				if got != want {
					t.Errorf("index %d: response crossed: got=%+v want=%+v", want.Index, got, want)
				}
			}(want)
		}
		close(start)
		wg.Wait()
	}
}
