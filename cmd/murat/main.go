package main

import (
	"fmt"
	"os"

	"lehnert.dev/murat/internal/app"
)

func main() {
	if err := app.Main(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "murat: %v\n", err)
		os.Exit(1)
	}
}
