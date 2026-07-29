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
	flags.Usage = func() {
		fmt.Fprintln(stdout, "Usage: nvidia-router [--help]")
	}

	if err := flags.Parse(args); err != nil {
		return err
	}
	_, err := New(context.Background(), Dependencies{})
	return err
}
