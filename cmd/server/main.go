// Command server runs the docvault HTTP API (and serves the built frontend if present).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hoveychen/docvault/internal/api"
	"github.com/hoveychen/docvault/internal/app"
	"github.com/hoveychen/docvault/internal/config"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.Build(ctx, cfg, log)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	defer a.Close()

	root := http.NewServeMux()
	apiHandler := api.NewRouter(a)
	root.Handle("/api/", apiHandler)
	root.Handle("/healthz", apiHandler)
	root.Handle("/", spaHandler())

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("server listening", "addr", cfg.HTTPAddr, "public_url", cfg.PublicURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// spaHandler serves the built single-page app from web/dist, falling back to
// index.html for client-side routes. If the build is absent it shows a hint.
func spaHandler() http.Handler {
	dist := "web/dist"
	index := filepath.Join(dist, "index.html")
	fileServer := http.FileServer(http.Dir(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(index); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><body style="font-family:sans-serif;padding:2rem">
<h1>docvault API is running</h1>
<p>Frontend not built yet. Run <code>make web-install &amp;&amp; make web-dev</code> for dev,
or <code>cd web &amp;&amp; pnpm build</code> to produce <code>web/dist</code>.</p>
</body></html>`))
			return
		}
		// Serve real files; fall back to index.html for unknown (SPA) paths.
		path := filepath.Join(dist, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}
