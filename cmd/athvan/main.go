// Command athvan serves the wallpaper API, or writes its OpenAPI document.
//
//	athvan                       serve on :8080 (PORT or -addr to change)
//	athvan openapi [-o file]     write the OpenAPI 3.1 YAML
//	athvan openapi -3.0 -o file  write it downgraded to OpenAPI 3.0.3
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones on hosts without them (Windows, scratch images)

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/satlavida/athvan/internal/api"
	"github.com/satlavida/athvan/internal/web"
)

//go:generate go run . openapi -o ../../openapi.yaml

func main() {
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		if err := writeSpec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := serve(os.Args[1:]); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func newAPI(mux *http.ServeMux) huma.API {
	a := humago.New(mux, api.Config())
	api.Register(a)
	return a
}

func writeSpec(args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	out := fs.String("o", "", "output file (default stdout)")
	v30 := fs.Bool("3.0", false, "downgrade to OpenAPI 3.0.3 for older tooling")
	jsonOut := fs.Bool("json", false, "write JSON instead of YAML")
	fs.Parse(args)

	oapi := newAPI(http.NewServeMux()).OpenAPI()
	var (
		b   []byte
		err error
	)
	switch {
	case *v30 && *jsonOut:
		b, err = oapi.Downgrade()
	case *v30:
		b, err = oapi.DowngradeYAML()
	case *jsonOut:
		b, err = oapi.MarshalJSON()
	default:
		b, err = oapi.YAML()
	}
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = os.Stdout.Write(b)
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("athvan", flag.ExitOnError)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := fs.String("addr", ":"+port, "listen address")
	fs.Parse(args)

	mux := http.NewServeMux()
	newAPI(mux)
	mux.Handle("GET /{$}", web.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	host := *addr
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	slog.Info("athvan listening", "preview", "http://"+host+"/", "docs", "http://"+host+"/docs")
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}
