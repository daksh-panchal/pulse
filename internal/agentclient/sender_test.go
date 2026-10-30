package agentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
)

func TestSenderPostsBatch(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		method, path = request.Method, request.URL.Path
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()

	sender, err := New(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := sender.Send(context.Background(), telemetry.Batch{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !accepted || method != http.MethodPost || path != "/api/v1/telemetry/batches" {
		t.Fatalf("accepted=%t method=%s path=%s", accepted, method, path)
	}
}
