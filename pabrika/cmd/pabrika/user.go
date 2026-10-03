package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

func runUser(ctx context.Context, args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprintln(e.stderr, "pabrika: user needs a subcommand: create or reset-password")
		return 2
	}
	switch args[0] {
	case "create":
		return runUserCreate(ctx, args[1:], e)
	case "reset-password":
		return runUserResetPassword(ctx, args[1:], e)
	default:
		fmt.Fprintf(e.stderr, "pabrika: unknown user subcommand %q (want create or reset-password)\n", args[0])
		return 2
	}
}

// newFlagSet builds a stdlib flag set. The stdlib stops parsing at the first positional
// argument, so flags must precede positionals; that is documented and tested, not worked around.
func newFlagSet(name string, e env) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	return fs
}

// parseFlags returns (exit code, true) when the caller must return that code.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, true
		}
		return 2, true
	}
	return 0, false
}

// openServices opens the store the running server uses (WAL and busy timeout make that safe)
// and runs migrations first, so the commands work on a fresh volume.
func openServices(ctx context.Context, e env) (*store.Store, *service.Services, error) {
	cfg, err := config.Load(e.getenv)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	if _, err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return st, service.New(st, service.Deps{}), nil
}

// readPassword takes the password from one line of stdin (fromStdin) or from a no-echo prompt
// on a terminal. It never comes from an argument. With neither a flag nor a terminal it fails.
func readPassword(e env, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(e.stdin).ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	f, ok := e.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", errors.New("stdin is not a terminal: pass --password-stdin and send the password on stdin")
	}
	fmt.Fprint(e.stderr, "Password: ")
	pw, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(e.stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(e.stderr, "Confirm password: ")
	again, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(e.stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(pw) != string(again) {
		return "", errors.New("passwords do not match")
	}
	return string(pw), nil
}

// printFields writes validation problems, one per line, in a stable order.
func printFields(w io.Writer, fields map[string]string) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "pabrika: %s: %s\n", k, fields[k])
	}
}

// runUserCreate is `pabrika user create --email E --name N [--password-stdin]`.
func runUserCreate(ctx context.Context, args []string, e env) int {
	fs := newFlagSet("user create", e)
	email := fs.String("email", "", "account email")
	name := fs.String("name", "", "display name")
	fromStdin := fs.Bool("password-stdin", false, "read the password from one line on stdin")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	if fs.NArg() > 0 || *email == "" || *name == "" {
		fmt.Fprintln(e.stderr, "usage: pabrika user create --email E --name N [--password-stdin]")
		return 2
	}
	pw, err := readPassword(e, *fromStdin)
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika:", err)
		return 1
	}
	st, svc, err := openServices(ctx, e)
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika:", err)
		return 1
	}
	defer st.Close()
	// Same validation as signup, all problems together.
	fields := map[string]string{}
	if verr := svc.Users.ValidateNew(*email, *name); verr != nil {
		for k, v := range verr.Fields {
			fields[k] = v
		}
	}
	if msg := auth.PasswordProblem(pw); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		printFields(e.stderr, fields)
		return 1
	}
	hash, err := auth.NewHasher(e.hashParams, 1).Hash(ctx, pw)
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika: hash password:", err)
		return 1
	}
	u, err := svc.Users.Create(ctx, *email, *name, hash)
	if err != nil {
		var se *service.Error
		switch {
		case errors.As(err, &se) && se.Code == "email_taken":
			fmt.Fprintf(e.stderr, "pabrika: an account with the email %s already exists\n", service.NormalizeEmail(*email))
		case errors.As(err, &se) && se.Kind == service.KindValidation:
			printFields(e.stderr, se.Fields)
		default:
			fmt.Fprintln(e.stderr, "pabrika: create user:", err)
		}
		return 1
	}
	fmt.Fprintf(e.stdout, "created user %s (%s)\n", u.Email, u.ID)
	return 0
}

// runUserResetPassword is `pabrika user reset-password [--password-stdin] EMAIL`. It deletes
// every session of the user; API tokens are not revoked (revoke them manually if the account
// is compromised).
func runUserResetPassword(ctx context.Context, args []string, e env) int {
	fs := newFlagSet("user reset-password", e)
	fromStdin := fs.Bool("password-stdin", false, "read the new password from one line on stdin")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(e.stderr, "usage: pabrika user reset-password [--password-stdin] EMAIL   (flags go before EMAIL)")
		return 2
	}
	email := fs.Arg(0)

	st, svc, err := openServices(ctx, e)
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika:", err)
		return 1
	}
	defer st.Close()
	userID, _, err := svc.Users.Credentials(ctx, email)
	if errors.Is(err, service.ErrNotFound) {
		fmt.Fprintf(e.stderr, "pabrika: no account with the email %s\n", service.NormalizeEmail(email))
		return 1
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika:", err)
		return 1
	}
	pw, err := readPassword(e, *fromStdin)
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika:", err)
		return 1
	}
	if msg := auth.PasswordProblem(pw); msg != "" {
		printFields(e.stderr, map[string]string{"password": msg})
		return 1
	}
	hash, err := auth.NewHasher(e.hashParams, 1).Hash(ctx, pw)
	if err != nil {
		fmt.Fprintln(e.stderr, "pabrika: hash password:", err)
		return 1
	}
	if err := svc.Users.SetPassword(ctx, userID, hash, ""); err != nil {
		fmt.Fprintln(e.stderr, "pabrika: set password:", err)
		return 1
	}
	fmt.Fprintf(e.stdout, "password reset for %s; all sessions revoked (API tokens are unchanged)\n", service.NormalizeEmail(email))
	return 0
}
