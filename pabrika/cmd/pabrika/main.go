// Command pabrika is the Pabrika server and CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/store"
)

// version is overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		if err := serve(context.Background(), getenv, stderr); err != nil {
			fmt.Fprintln(stderr, "pabrika: serve:", err)
			return 1
		}
		return 0
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "pabrika: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: pabrika <command>\n\ncommands:\n  serve     start the server (phase 1: load config, open and migrate the database, exit)\n  version   print the version")
}

// serve is a bootstrap in phase 1: config, open, migrate, log, exit. HTTP arrives in phase 2.
func serve(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	log := slog.New(slog.NewTextHandler(logOut, nil))
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	v, err := st.Migrate(ctx)
	if err != nil {
		return err
	}
	log.Info("database ready", "path", cfg.DBPath, "schema_version", v, "version", version)
	return nil
}
