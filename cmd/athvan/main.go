// Command athvan serves the wallpaper API, or writes its OpenAPI document.
//
//	athvan                       serve on :8080 (PORT or -addr to change)
//	athvan openapi [-o file]     write the OpenAPI 3.1 YAML
//	athvan openapi -3.0 -o file  write it downgraded to OpenAPI 3.0.3
//
// Environment:
//
//	ATHVAN_ADMIN_USER, ATHVAN_ADMIN_PASSWORD  enable /admin and require API keys
//	ATHVAN_REQUIRE_KEY                        override whether keys are required
//	ATHVAN_DATA_DIR                           where keys.json lives (default ./data)
//	ATHVAN_ALLOW_PRIVATE_URLS=1               let image_url reach private addresses
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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones on hosts without them (Windows, scratch images)

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/satlavida/athvan/internal/admin"
	"github.com/satlavida/athvan/internal/api"
	"github.com/satlavida/athvan/internal/keys"
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

func newAPI(mux *http.ServeMux, authorize api.Authorizer) huma.API {
	a := humago.New(mux, api.Config())
	api.Register(a, authorize)
	return a
}

func writeSpec(args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	out := fs.String("o", "", "output file (default stdout)")
	v30 := fs.Bool("3.0", false, "downgrade to OpenAPI 3.0.3 for older tooling")
	jsonOut := fs.Bool("json", false, "write JSON instead of YAML")
	fs.Parse(args)

	oapi := newAPI(http.NewServeMux(), nil).OpenAPI()
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

	dataDir := os.Getenv("ATHVAN_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	store, err := keys.Open(filepath.Join(dataDir, "keys.json"))
	if err != nil {
		return fmt.Errorf("open key store: %w", err)
	}
	adm := admin.New(store, os.Getenv("ATHVAN_ADMIN_USER"), os.Getenv("ATHVAN_ADMIN_PASSWORD"))

	// Keys are required once an admin exists to hand them out, unless
	// ATHVAN_REQUIRE_KEY says otherwise.
	requireKey := adm.Enabled()
	if v := os.Getenv("ATHVAN_REQUIRE_KEY"); v != "" {
		requireKey, err = strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("ATHVAN_REQUIRE_KEY: %w", err)
		}
	}
	errNoKey := errors.New("API key required: send Authorization: Bearer <key>, X-API-Key, or ?key=")
	errBadKey := errors.New("API key is invalid or revoked")
	authorize := func(r *http.Request) error {
		if !requireKey || adm.IsAdmin(r) {
			return nil
		}
		k := keys.FromRequest(r)
		if k == "" {
			return errNoKey
		}
		if _, ok := store.Verify(k); !ok {
			return errBadKey
		}
		return nil
	}

	mux := http.NewServeMux()
	newAPI(mux, authorize)
	adm.Register(mux)
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
	go store.Run(ctx, 5*time.Second)
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
	slog.Info("athvan listening", "preview", "http://"+host+"/", "docs", "http://"+host+"/docs", "admin", "http://"+host+"/admin")
	switch {
	case !adm.Enabled():
		slog.Warn("admin disabled: set ATHVAN_ADMIN_USER and ATHVAN_ADMIN_PASSWORD to issue API keys")
	case !requireKey:
		slog.Warn("API keys are not required (ATHVAN_REQUIRE_KEY=false)")
	}
	if !requireKey {
		slog.Warn("the API is open to anyone who can reach it")
	}
	err = srv.ListenAndServe()
	if ferr := store.Flush(); ferr != nil {
		slog.Error("flush key usage", "err", ferr)
	}
	if !errors.Is(err, http.ErrServerClosed) {
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
