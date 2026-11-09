package main

import (
	"context"
	"fmt"
	"os"

	"campus-hive/backend-go/internal/schemadiff"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: schemadiff <candidate-database-url> <reference-database-url>")
		os.Exit(2)
	}
	ctx := context.Background()

	candidate, err := schemadiff.Load(ctx, os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "candidate: %v\n", err)
		os.Exit(1)
	}
	reference, err := schemadiff.Load(ctx, os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference: %v\n", err)
		os.Exit(1)
	}

	differences := schemadiff.Compare(candidate, reference)
	if len(differences) == 0 {
		fmt.Printf("SCHEMA IDENTICAL (%s)\n", candidate.Summary())
		return
	}
	for _, line := range differences {
		fmt.Println(line)
	}
	fmt.Fprintf(os.Stderr, "SCHEMA DRIFT DETECTED: %d difference(s)\n", len(differences))
	os.Exit(1)
}
