package main

import (
	"context"
	"fmt"
	"os"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func usage() error {
	return fmt.Errorf("usage: migrate <up|down|reset|version|latest|force>")
}

func run() error {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "latest":
		version, err := migrations.LatestVersion()
		if err != nil {
			return err
		}
		fmt.Printf("latest=%d\n", version)
		return nil
	}

	databaseURL, err := config.LoadDatabaseURL()
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch command {
	case "up":
		version, err := migrations.Up(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("schema at version %d\n", version)
	case "down":
		version, err := migrations.DownAll(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("schema reverted to version %d\n", version)
	case "reset":
		if err := migrations.Reset(ctx, databaseURL); err != nil {
			return err
		}
		version, _, err := migrations.Version(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("schema reset to version %d\n", version)
	case "version":
		version, dirty, err := migrations.Version(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
	case "force":
		version, err := migrations.Force(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("migration bookkeeping cleared; the next migration is %d\n", version)
		fmt.Fprintln(os.Stderr, "warning: force does not undo a partially applied migration; "+
			"verify the schema or recreate the database before re-applying")
	default:
		return usage()
	}
	return nil
}
