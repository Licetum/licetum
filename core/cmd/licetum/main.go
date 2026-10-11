// licetum is the runtime of Licetum, the part client services call on every request:
//
//	licetum check             — permission-check API (default :8001)
//	licetum migrate           — apply DB migrations and seed the self-service
//	licetum openapi check [-yaml] [-lang en|ru|zh] — print the OpenAPI spec (no DB or Redis needed)
//
// The admin API is a separate binary (admin-api/cmd/licetum-admin). The address can be overridden with the -addr flag or
// the HTTP__ADDR variable.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/cli"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/server"
)

const usage = `Usage: licetum <command> [flags]

Commands:
  check     run the permission-check API (default :8001)
  migrate   apply DB migrations and seed the self-service
  openapi   print the OpenAPI spec: licetum openapi check [-yaml] [-lang en|ru|zh]
`

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG__LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	// The process logs through logx: its format, fields and sampling come from the admin API once the settings are
	// loaded (see cli.Serve); until then the defaults below apply.
	logs, logErr := logx.New(os.Stdout, logx.DefaultOptions(logLevel))
	if logErr != nil {
		fmt.Fprintln(os.Stderr, "logging:", logErr)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(logs))

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd := os.Args[1]; cmd {
	case "check":
		err = cli.Serve(ctx, "check", os.Args[2:], ":8001", func(app *engine.App) http.Handler {
			return server.NewCheckHandler(app)
		}, logs)
	case "migrate":
		err = cli.Migrate(ctx)
	case "openapi":
		err = cli.PrintOpenAPI(os.Args[2:], map[string]func() huma.API{
			"check": func() huma.API {
				_, api := server.NewCheckAPI(&engine.App{Cfg: &config.Config{}})
				return api
			},
		})
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
