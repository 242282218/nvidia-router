package app

import (
	"context"
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

func runCLI(args []string, stdout, _ io.Writer) error {
	if len(args) == 1 && args[0] == "--help" {
		if _, err := fmt.Fprintln(stdout, "Usage: nvidia-router [--help]"); err != nil {
			return fmt.Errorf("write usage: %w", err)
		}
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("unexpected argument: %s", args[0])
	}
	_, err := New(context.Background(), Dependencies{})
	return err
}
