package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardAssetsAreEmbedded(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html", contains: "Pulse infrastructure monitoring dashboard"},
		{path: "/assets/styles.css", contentType: "text/css", contains: "--color-surface"},
		{path: "/assets/main.js", contentType: "text/javascript", contains: "/api/v1/containers"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", test.path, response.Code)
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) {
			t.Errorf("GET %s content type = %q", test.path, response.Header().Get("Content-Type"))
		}
		if !strings.Contains(response.Body.String(), test.contains) {
			t.Errorf("GET %s did not contain %q", test.path, test.contains)
		}
	}
}
