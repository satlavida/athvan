// Package web serves the interactive preview page.
package web

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var index []byte

// Handler serves the preview page.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
