// Command swic is a small read-only web UI for a Calibre library.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jvoisin/swic/internal/calibre"
	"github.com/jvoisin/swic/internal/web"
)

func main() {
	var (
		libraryPath = flag.String("library", "", "path to the Calibre library directory (required)")
		addr        = flag.String("addr", ":8080", "address to listen on")
		pageSize    = flag.Int("page-size", 50, "number of books per page")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *libraryPath == "" {
		logger.Error("missing required -library flag")
		flag.Usage()
		os.Exit(2)
	}

	lib, err := calibre.Open(*libraryPath)
	if err != nil {
		logger.Error("open calibre library", "err", err)
		os.Exit(1)
	}
	defer lib.Close()

	srv, err := web.New(lib, logger, *pageSize)
	if err != nil {
		logger.Error("init web server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute, // large book downloads
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 * 1024,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("listening", "addr", *addr, "library", lib.Root())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}
