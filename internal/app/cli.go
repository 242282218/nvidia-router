package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

func RunCLI(args []string) {
	if err := runCLI(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
		return err
	}
	_, err := New(context.Background(), Dependencies{})
	return err
}
