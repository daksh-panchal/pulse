package dashboard

import (
	_ "embed"
	"net/http"
)

var (
	//go:embed dist/pulse-shell.html
	page []byte

	//go:embed dist/styles.css
	styles []byte

	//go:embed dist/main.js
	script []byte
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", serve("text/html; charset=utf-8", page))
	mux.HandleFunc("GET /assets/styles.css", serve("text/css; charset=utf-8", styles))
	mux.HandleFunc("GET /assets/main.js", serve("text/javascript; charset=utf-8", script))
}

func serve(contentType string, content []byte) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", contentType)
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(content)
	}
}
