// Package cli holds what the Licetum binaries share: reading the configuration, applying the migrations, serving an
// HTTP application with its settings and printing the OpenAPI document. The binaries themselves (the check runtime
// in core/cmd/licetum, the admin API in admin-api/cmd/licetum-admin) only choose the subcommands.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/redisx"
	"github.com/licetum/licetum/core/server"
)

// LoadConfig reads the environment; with requireAll the Redis and the auth settings must be set too.
func LoadConfig(requireAll bool) (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.RequireDB(); err != nil {
		return nil, err
	}
	if requireAll {
		if err := errors.Join(cfg.RequireRedis(), cfg.RequireAuth()); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// Migrate applies the database migrations and seeds the self-service.
func Migrate(ctx context.Context) error {
	cfg, err := LoadConfig(false)
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool, cfg)
}

// Serve runs an HTTP application until the context ends. name is the application's name in the logs, defaultAddr its
// address when neither -addr nor HTTP__ADDR is set; build creates the handler from the engine.
func Serve(ctx context.Context, name string, args []string, defaultAddr string, build func(*engine.App) http.Handler, logs *logx.Switch) error {
	cfg, err := LoadConfig(true)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet(name, flag.ExitOnError)
	addr := fs.String("addr", "", "HTTP server address (overrides HTTP__ADDR)")
	_ = fs.Parse(args)
	switch {
	case *addr != "":
	case cfg.HTTP.Addr != "":
		*addr = cfg.HTTP.Addr
	default:
		*addr = defaultAddr
	}

	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The check API answers within a budget: its Redis client times out fast, does not retry, and skips Redis for a while
	// after repeated failures (see redisx.FastFail). The admin API waits for Redis as long as the default allows.
	var redisOptions redisx.Options
	if name == "check" && cfg.Redis.HotTimeout > 0 {
		redisOptions.HotTimeout = cfg.Redis.HotTimeout
	}
	rdb, err := redisx.ConnectWith(ctx, cfg.Redis.URI, redisOptions)
	if err != nil {
		return err
	}
	defer rdb.Close()

	app := engine.New(pool, rdb, cfg)
	defer app.Close()
	app.Logs = logs
	if err := app.LoadLogSettings(ctx); err != nil {
		slog.WarnContext(ctx, "the stored log settings are not applied, the defaults stay in force", "error", err)
	}
	go app.WatchLogSettings(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           build(app),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "app", name, "addr", *addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// PrintOpenAPI prints the spec of an application without connecting to the DB or Redis: registering routes does not need
// those dependencies. args is "<name> [-yaml] [-lang en|ru|zh]"; apps maps each name to the function that builds its API.
func PrintOpenAPI(args []string, apps map[string]func() huma.API) error {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	asYAML := fs.Bool("yaml", false, "print YAML instead of JSON")
	langCode := fs.String("lang", string(i18n.Default), "language of descriptions: en, ru or zh")
	build, ok := apps[firstArg(args)]
	if !ok {
		return fmt.Errorf("usage: openapi <%s> [-yaml] [-lang en|ru|zh]", joinNames(apps))
	}
	_ = fs.Parse(args[1:])
	lang, ok := i18n.FromCode(*langCode)
	if !ok {
		return fmt.Errorf("unsupported language %q (supported: en, ru, zh)", *langCode)
	}

	out, err := server.LocalizedSpec(build(), lang, *asYAML)
	if err != nil {
		return err
	}
	if !*asYAML {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, out, "", "  "); err != nil {
			return err
		}
		out = pretty.Bytes()
	}
	_, err = os.Stdout.Write(append(out, '\n'))
	return err
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func joinNames(apps map[string]func() huma.API) string {
	out := ""
	for name := range apps {
		if out != "" {
			out += "|"
		}
		out += name
	}
	return out
}
