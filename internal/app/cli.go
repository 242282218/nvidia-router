package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/database"
)

const (
	defaultCLIDataDir = "/data"
	routerDBFilename  = "router.db"
	cliUsage          = "Usage:\n" +
		"  nvidia-router [--help]\n" +
		"  nvidia-router serve\n" +
		"  nvidia-router admin reset-password --password <new>\n" +
		"  nvidia-router db backup --output <path>\n"
)

func RunCLI(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCLIContext(ctx, args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout, _ io.Writer) error {
	return runCLIContext(context.Background(), args, stdout, io.Discard)
}

func runCLIContext(ctx context.Context, args []string, stdout, _ io.Writer) error {
	if len(args) == 1 && args[0] == "--help" {
		if _, err := fmt.Fprint(stdout, cliUsage); err != nil {
			return fmt.Errorf("write usage: %w", err)
		}
		return nil
	}
	if len(args) == 4 && args[0] == "admin" && args[1] == "reset-password" && args[2] == "--password" {
		return runAdminPasswordReset(ctx, args[3], stdout)
	}
	if len(args) == 4 && args[0] == "db" && args[1] == "backup" && args[2] == "--output" {
		return runDatabaseBackup(ctx, args[3], stdout)
	}
	if len(args) == 1 && args[0] == "serve" {
		return runServe(ctx)
	}
	if len(args) != 0 {
		return errors.New("invalid command; run --help for usage")
	}
	application, err := New(ctx, Dependencies{})
	if err != nil {
		return err
	}
	if err := application.Close(); err != nil {
		return fmt.Errorf("close initialized application: %w", err)
	}
	return nil
}

func runServe(ctx context.Context) error {
	application, err := New(ctx, Dependencies{})
	if err != nil {
		return fmt.Errorf("initialize serve application: %w", err)
	}
	if err := application.Serve(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func runAdminPasswordReset(ctx context.Context, password string, stdout io.Writer) error {
	db, err := openExistingRouterDatabase()
	if err != nil {
		return err
	}
	operationErr := adminauth.NewRepository(db, nil).ResetPassword(ctx, password)
	if err := closeCLIData(db, operationErr); err != nil {
		return fmt.Errorf("reset administrator password: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, "Administrator password reset; all sessions revoked."); err != nil {
		return fmt.Errorf("write password reset result: %w", err)
	}
	return nil
}

func runDatabaseBackup(ctx context.Context, output string, stdout io.Writer) error {
	databasePath, err := routerDatabasePath()
	if err != nil {
		return err
	}
	same, err := sameDatabaseFile(databasePath, output)
	if err != nil {
		return fmt.Errorf("validate database backup output: %w", err)
	}
	if same {
		return errors.New("database backup output must differ from router database")
	}
	db, err := openExistingRouterDatabase()
	if err != nil {
		return err
	}
	operationErr := database.Backup(ctx, db, output)
	if err := closeCLIData(db, operationErr); err != nil {
		return fmt.Errorf("backup router database: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, "Database backup completed."); err != nil {
		return fmt.Errorf("write database backup result: %w", err)
	}
	return nil
}

func openExistingRouterDatabase() (*sql.DB, error) {
	databasePath, err := routerDatabasePath()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(databasePath)
	if err != nil {
		return nil, fmt.Errorf("find router database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("find router database: path is not a regular file")
	}
	db, err := database.Open(databasePath)
	if err != nil {
		return nil, fmt.Errorf("open router database: %w", err)
	}
	return db, nil
}

func routerDatabasePath() (string, error) {
	dataDir := os.Getenv("NVIDIA_ROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultCLIDataDir
	}
	databasePath, err := filepath.Abs(filepath.Join(dataDir, routerDBFilename))
	if err != nil {
		return "", fmt.Errorf("resolve router database path: %w", err)
	}
	return databasePath, nil
}

func sameDatabaseFile(source, output string) (bool, error) {
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return false, fmt.Errorf("resolve output path: %w", err)
	}
	pathsEqual := filepath.Clean(source) == filepath.Clean(outputPath)
	if runtime.GOOS == "windows" {
		pathsEqual = strings.EqualFold(filepath.Clean(source), filepath.Clean(outputPath))
	}
	if pathsEqual {
		return true, nil
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return false, fmt.Errorf("stat router database: %w", err)
	}
	outputInfo, err := os.Stat(outputPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat output path: %w", err)
	}
	return os.SameFile(sourceInfo, outputInfo), nil
}

func closeCLIData(db *sql.DB, operationErr error) error {
	if closeErr := db.Close(); closeErr != nil {
		return errors.Join(operationErr, fmt.Errorf("close router database: %w", closeErr))
	}
	return operationErr
}
