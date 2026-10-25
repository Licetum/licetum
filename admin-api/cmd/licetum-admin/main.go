// licetum-admin is the administration of Licetum: the admin API and the commands that go with it.
//
//	licetum-admin admin                         — admin API (default :8000)
//	licetum-admin restore-admin [-user name]... — put the access of the platform administrators back
//	licetum-admin openapi admin [-yaml] [-lang en|ru|zh] — print the OpenAPI spec (no DB or Redis needed)
//
// The database is migrated by the core binary (licetum migrate). The address can be overridden with -addr or HTTP__ADDR.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/admin-api/api"
	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/cli"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/logx"
)

const usage = `Usage: licetum-admin <command> [flags]

Commands:
  admin          run the admin API (default :8000)
  restore-admin  put the platform administrators' access back: licetum-admin restore-admin [-user name]...
  openapi        print the OpenAPI spec: licetum-admin openapi admin [-yaml] [-lang en|ru|zh]
`

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG__LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	// The process logs through logx; its format, fields and sampling come from the stored settings (see cli.Serve).
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
	case "admin":
		err = cli.Serve(ctx, "admin", os.Args[2:], ":8000", func(core *engine.App) http.Handler {
			return api.NewAdminHandler(service.New(core))
		}, logs)
	case "restore-admin":
		err = restoreAdmin(ctx, os.Args[2:])
	case "openapi":
		err = cli.PrintOpenAPI(os.Args[2:], map[string]func() huma.API{
			"admin": func() huma.API {
				_, a := api.NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
				return a
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

// users is a repeatable -user flag.
type users []string

func (u *users) String() string     { return fmt.Sprint([]string(*u)) }
func (u *users) Set(v string) error { *u = append(*u, v); return nil }

// restoreAdmin is the way back in when nobody can manage the platform any more. It needs the database only: it bumps the
// revision of the rules in PostgreSQL, which every PDP follows, so the answers made before it are not served again.
func restoreAdmin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore-admin", flag.ExitOnError)
	var also users
	fs.Var(&also, "user", "also make this user (preferred_username) an administrator; repeatable")
	_ = fs.Parse(args)

	cfg, err := cli.LoadConfig(false)
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	report, err := db.RestoreAdminAccess(ctx, pool, cfg, also)
	if err != nil {
		return err
	}
	slog.Warn("administrator access restored", "memberships", report.Members, "denials_switched_off", report.DisabledDenials)

	return nil
}
