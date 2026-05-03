package main

import (
	"fmt"
	"os"

	"github.com/mrgeoffrich/mini-kanban/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "mk:", err)
		os.Exit(1)
	}
}
