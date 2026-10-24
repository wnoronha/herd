package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/wnoronha/herd/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		var exitErr cli.ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
