// Command pabrika is the Pabrika server and CLI.
//
//	pabrika serve
//	pabrika user create --email E --name N [--password-stdin]
//	pabrika user reset-password [--password-stdin] EMAIL
//	pabrika healthcheck
//	pabrika version
//
// Exit codes: 0 success, 1 runtime error, 2 usage error. Messages go to stderr.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/RJGJ/Pabrika/internal/auth"
)

// version is overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// env is everything a command touches outside its arguments, so tests can inject it.
type env struct {
	getenv         func(string) string
	stdin          io.Reader
	stdout, stderr io.Writer
	hashParams     auth.Params // argon2 cost; DefaultParams in production
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], env{
		getenv: os.Getenv, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		hashParams: auth.DefaultParams,
	})
	stop()
	os.Exit(code)
}

// run dispatches on the first argument and returns the process exit code.
func run(ctx context.Context, args []string, e env) int {
	if e.hashParams == (auth.Params{}) {
		e.hashParams = auth.DefaultParams
	}
	if len(args) == 0 {
		usage(e.stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		if len(args) > 1 {
			fmt.Fprintln(e.stderr, "pabrika: serve takes no arguments (configuration is by environment variables)")
			return 2
		}
		if err := serve(ctx, e, defaultShutdownTimeout); err != nil {
			fmt.Fprintln(e.stderr, "pabrika: serve:", err)
			return 1
		}
		return 0
	case "user":
		return runUser(ctx, args[1:], e)
	case "healthcheck":
		return runHealthcheck(e.getenv, e.stderr, healthcheckTimeout)
	case "version":
		fmt.Fprintln(e.stdout, version)
		return 0
	case "help", "-h", "--help":
		usage(e.stdout)
		return 0
	default:
		fmt.Fprintf(e.stderr, "pabrika: unknown command %q\n", args[0])
		usage(e.stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: pabrika <command>

commands:
  serve                                          start the server (configured by environment variables)
  user create --email E --name N [--password-stdin]
                                                 create an account (works even when ALLOW_SIGNUP=false)
  user reset-password [--password-stdin] EMAIL   set a new password and revoke the user's sessions
  healthcheck                                    GET /healthz on 127.0.0.1:$PORT; exit 0 when healthy
  version                                        print the version

Flags must come before positional arguments. Passwords are never taken from arguments:
use --password-stdin (one line on stdin) or the interactive prompt.
`)
}
