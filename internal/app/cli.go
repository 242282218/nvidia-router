package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func RunCLI(args []string) {
	if err := runCLI(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("nvidia-router", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var usageErr error
	flags.Usage = func() {
		if _, err := fmt.Fprintln(stdout, "Usage: nvidia-router [--help]"); err != nil {
			usageErr = fmt.Errorf("write usage: %w", err)
		}
	}

	if err := flags.Parse(args); err != nil {
		if usageErr != nil {
			return usageErr
		}
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	_, err := New(context.Background(), Dependencies{})
	return err
}
