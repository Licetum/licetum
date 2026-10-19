// licetum is the runtime of Licetum, the part client services call on every request:
//
//	licetum pdp               — the regional PDP: the AuthZEN API (default :8001), probes and metrics on :9090
//	licetum worker            — synchronization worker: executes the jobs that fetch the resource sources (default :8002, metrics)
//	licetum migrate           — apply DB migrations and seed the self-service
//	licetum failover          — raise every revision after a replica was promoted (see docs/deployment.md)
//	licetum openapi pdp [-yaml] [-lang en|ru|zh] — print the OpenAPI spec (no DB or Redis needed)
//
// The admin API is a separate binary (admin-api/cmd/licetum-admin). The PDP's address can be overridden with the -addr flag
// or the PDP__ADDR variable; the worker reads HTTP__ADDR.
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
  pdp       run the regional PDP: the AuthZEN API (default :8001); probes and metrics on PDP__OPS_ADDR (default :9090)
  worker    run the synchronization worker (metrics on :8002)
  migrate   apply DB migrations and seed the self-service
  failover  raise every revision of the policy after a replica was promoted (docs/deployment.md)
  openapi   print the OpenAPI spec: licetum openapi pdp [-yaml] [-lang en|ru|zh]
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
	case "pdp":
		err = cli.Serve(ctx, "pdp", os.Args[2:], ":8001", func(app *engine.App) http.Handler {
			return server.NewPDPHandler(app)
		}, logs)
	case "worker":
		err = cli.Worker(ctx, os.Args[2:], ":8002", logs)
	case "migrate":
		err = cli.Migrate(ctx)
	case "failover":
		err = cli.Failover(ctx, os.Args[2:])
	case "openapi":
		err = cli.PrintOpenAPI(os.Args[2:], map[string]func() huma.API{
			"pdp": func() huma.API {
				_, api := server.NewPDPAPI(&engine.App{Cfg: &config.Config{}})
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
