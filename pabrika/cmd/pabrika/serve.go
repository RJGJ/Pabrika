package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/httpapi"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

// defaultShutdownTimeout is 8 s (not 10) so `docker stop`, whose grace period is 10 s, sees a
// clean exit 0 instead of killing the process (exit 137).
const defaultShutdownTimeout = 8 * time.Second

// Server timeouts. There is deliberately NO WriteTimeout: it would cut event streams (SSE).
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

// serveParams is everything runServer needs, so it can be tested without a database or the
// real handler.
type serveParams struct {
	// Listener is used when set; otherwise runServer listens on Addr.
	Listener net.Listener
	Addr     string

	Handler http.Handler
	Log     *slog.Logger

	// StartBackground starts the background jobs (session purge, limiter sweeps) and returns a
	// channel closed once they have stopped after ctx is cancelled. May be nil.
	StartBackground func(ctx context.Context) <-chan struct{}
	// ShutdownHook runs before srv.Shutdown. Phase 3 supplies the event hub's Shutdown here:
	// srv.Shutdown does not cancel open streams, so the hub must end them first. May be nil.
	ShutdownHook func()
	// Close is closed last (the store: WAL checkpoint). May be nil.
	Close io.Closer

	ShutdownTimeout time.Duration
}

// runServer serves until ctx is cancelled, then shuts down in the documented order
// (phase 2 spec 12.1):
//
//  1. stop the background jobs (purge, limiter sweeps);
//  2. run the shutdown hook (hub: ends open streams, new ones get 503);
//  3. srv.Shutdown with the deadline (stops accepting, waits for in-flight requests);
//  4. if that fails (deadline), srv.Close();
//  5. close the store, then return nil so the process exits 0.
//
// A serve error other than http.ErrServerClosed (for example the listener failing) is returned
// after the same cleanup.
func runServer(ctx context.Context, p serveParams) error {
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.ShutdownTimeout <= 0 {
		p.ShutdownTimeout = defaultShutdownTimeout
	}
	srv := &http.Server{
		Handler:           p.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		// No WriteTimeout, see above.
		ErrorLog: slog.NewLogLogger(p.Log.Handler(), slog.LevelWarn),
	}
	ln := p.Listener
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", p.Addr); err != nil {
			closeQuietly(p.Close, p.Log)
			return fmt.Errorf("listen %s: %w", p.Addr, err)
		}
	}

	bgCtx, stopBG := context.WithCancel(context.Background())
	var bgDone <-chan struct{}
	if p.StartBackground != nil {
		bgDone = p.StartBackground(bgCtx)
	}
	stopBackground := func() {
		stopBG()
		if bgDone != nil {
			<-bgDone
		}
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	p.Log.Info("listening", "addr", ln.Addr().String())

	select {
	case err := <-serveErr:
		stopBackground()
		closeQuietly(p.Close, p.Log)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	p.Log.Info("shutting down")
	stopBackground() // 1
	if p.ShutdownHook != nil {
		p.ShutdownHook() // 2
	}
	sctx, cancel := context.WithTimeout(context.Background(), p.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil { // 3
		p.Log.Warn("graceful shutdown incomplete, closing connections", "err", err)
		_ = srv.Close() // 4
	}
	<-serveErr
	closeQuietly(p.Close, p.Log) // 5
	p.Log.Info("stopped")
	return nil
}

func closeQuietly(c io.Closer, log *slog.Logger) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		log.Error("close store", "err", err)
	}
}

// serve is the `serve` command: config, store and migrations, services, HTTP server, then
// runServer until SIGINT/SIGTERM (ctx).
func serve(ctx context.Context, e env, shutdownTimeout time.Duration) error {
	log := slog.New(slog.NewTextHandler(e.stderr, nil))
	cfg, err := config.Load(e.getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			_ = st.Close()
		}
	}()
	v, err := st.Migrate(ctx)
	if err != nil {
		return err
	}
	log.Info("database ready", "path", cfg.DBPath, "schema_version", v, "version", version)

	hub := service.NewHub(service.HubOptions{Logger: log})
	svc := service.New(st, service.Deps{Publisher: hub, Streams: hub})
	sessions := auth.NewSessions(st, auth.SessionOptions{CookieSecure: cfg.CookieSecure, Logger: log})
	api := httpapi.New(httpapi.Deps{
		Config: cfg, Store: st, Services: svc, Logger: log, Sessions: sessions,
		Hasher: auth.NewHasher(e.hashParams, auth.DefaultConcurrency), Hub: hub,
	})

	handedOff = true // runServer closes the store from here on
	return runServer(ctx, serveParams{
		Addr:    ":" + strconv.Itoa(cfg.Port),
		Handler: api.Handler(),
		Log:     log,
		StartBackground: func(bg context.Context) <-chan struct{} {
			return joinDone(sessions.StartPurge(bg, nil), api.StartBackground(bg))
		},
		ShutdownHook:    hub.Shutdown,
		Close:           st,
		ShutdownTimeout: shutdownTimeout,
	})
}

// joinDone returns a channel closed when every input channel is closed.
func joinDone(chans ...<-chan struct{}) <-chan struct{} {
	out := make(chan struct{})
	var wg sync.WaitGroup
	for _, c := range chans {
		wg.Add(1)
		go func(c <-chan struct{}) { defer wg.Done(); <-c }(c)
	}
	go func() { wg.Wait(); close(out) }()
	return out
}
