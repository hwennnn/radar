package app

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/index.html web/docs.html web/styles.css web/app.js web/docs.js
var embeddedUI embed.FS

func registerUI(mux *http.ServeMux) {
	assets, err := fs.Sub(embeddedUI, "web")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/", "/index.html", "/jobs", "/companies", "/system", "/docs", "/docs.html", "/styles.css", "/app.js", "/docs.js":
		default:
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https://www.google.com https://*.gstatic.com; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasSuffix(request.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		if strings.HasSuffix(request.URL.Path, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		if request.URL.Path == "/jobs" || request.URL.Path == "/companies" || request.URL.Path == "/system" {
			request.URL.Path = "/"
		} else if request.URL.Path == "/docs" {
			request.URL.Path = "/docs.html"
		}
		files.ServeHTTP(w, request)
	})
}
