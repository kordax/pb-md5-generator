package studio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxCLIPasswordBytes = 1024

type cliActions struct {
	serve     func(context.Context, Config) error
	bootstrap func(context.Context, Config, string, string, []byte) error
}

// RunCLI executes serve or admin-user. args includes the command name.
// Password input is an exact UTF-8 byte stream terminated by EOF, not a line.
func RunCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runCLI(ctx, args, stdin, stdout, stderr, cliActions{serve: Run, bootstrap: Bootstrap})
}

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, actions cliActions) error {
	if ctx == nil || len(args) == 0 || len(args) > 64 {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	command := args[0]
	if command != "serve" && command != "admin-user" {
		return fmt.Errorf("%w: expected serve or admin-user", ErrInvalidConfig)
	}
	config := DefaultConfig("")
	flags := flag.NewFlagSet("pb-md5-generator "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: pb-md5-generator %s [flags]\n", command)
		flags.PrintDefaults()
	}
	flags.StringVar(&config.DataDir, "data-dir", config.DataDir, "dedicated persistent data directory (default: user config directory/pb-docs)")
	flags.StringVar(&config.PublicURL, "public-url", config.PublicURL, "public HTTPS URL, or HTTP URL with the literal localhost hostname; optional hosting prefix")
	var username, displayName string
	var passwordStdin bool
	if command == "serve" {
		flags.StringVar(&config.Address, "listen", config.Address, "HTTP bind address")
		flags.StringVar(&config.Backend, "storage", config.Backend, "content storage: local or git")
		flags.StringVar(&config.GitRemote, "git-remote", config.GitRemote, "configured Git remote name (not a URL)")
		flags.BoolVar(&config.Initialize, "init", config.Initialize, "initialize an empty dedicated content directory")
	} else {
		flags.StringVar(&username, "username", "", "first administrator username")
		flags.StringVar(&displayName, "display-name", "", "administrator display name (default: username)")
		flags.BoolVar(&passwordStdin, "password-stdin", false, "read the exact password bytes from standard input until EOF")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected positional arguments", ErrInvalidConfig)
	}
	prepared, err := prepareConfig(config)
	if err != nil {
		return err
	}
	config = prepared.Config
	if command == "serve" {
		if actions.serve == nil {
			return ErrInvalidConfig
		}
		if _, err := fmt.Fprintf(stdout, "Starting CMS at %s/_docs/admin/\n", strings.TrimSuffix(config.PublicURL, "/")); err != nil {
			return err
		}
		return actions.serve(ctx, config)
	}
	if !passwordStdin || username == "" || stdin == nil || actions.bootstrap == nil {
		return fmt.Errorf("%w: admin-user requires --username and --password-stdin", ErrInvalidConfig)
	}
	if displayName == "" {
		displayName = username
	}
	password, err := io.ReadAll(io.LimitReader(stdin, maxCLIPasswordBytes+1))
	defer clear(password)
	if err != nil {
		return errors.New("cannot read administrator password from standard input")
	}
	if len(password) == 0 || len(password) > maxCLIPasswordBytes || !utf8.Valid(password) {
		return errors.New("administrator password must contain 1–1024 valid UTF-8 bytes; send it without a trailing newline")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := actions.bootstrap(ctx, config, username, displayName, password); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Administrator created. Start the CMS with the same data directory.")
	return err
}
