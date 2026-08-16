package apiserver

import (
	"net/http"
	"strings"
)

func mountDocs(mux *http.ServeMux) {
	h := docsHandler()
	mux.HandleFunc("/docs", h)
	mux.HandleFunc("/docs/", h)
}

func docsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs":
			http.Redirect(w, r, "/docs/", http.StatusPermanentRedirect)
		case "/docs/":
			serveDocs(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func serveDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(docsHTML)
}

func isDocsPath(path string) bool {
	return path == "/docs" || strings.HasPrefix(path, "/docs/")
}
