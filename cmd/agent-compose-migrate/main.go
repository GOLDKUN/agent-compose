package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"agent-compose/cmd/agent-compose-migrate/migrate"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("agent-compose-migrate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var options migrate.Options
	flags.StringVar(&options.Source, "source", "", "source data root (required; opened read-only)")
	flags.StringVar(&options.Target, "target", "", "new target data root (required)")
	flags.StringVar(&options.RuntimeRoot, "runtime-root", "", "target data root as seen by the daemon (defaults to --target)")
	flags.BoolVar(&options.DryRun, "dry-run", false, "inspect and report without writing the target")
	flags.BoolVar(&options.JSON, "json", false, "write the report as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		return 2
	}
	report, err := migrate.Run(ctx, options)
	if options.JSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(report); encodeErr != nil {
			fmt.Fprintf(os.Stderr, "write migration report: %v\n", encodeErr)
			return 1
		}
	} else {
		if _, writeErr := fmt.Fprintln(os.Stdout, report.Text()); writeErr != nil {
			fmt.Fprintf(os.Stderr, "write migration report: %v\n", writeErr)
			return 1
		}
	}
	if err != nil {
		if !options.JSON && !errors.Is(err, migrate.ErrReported) {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	return 0
}
