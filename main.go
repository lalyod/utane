package main

import (
	"log"
	"os"

	"github.com/lalyod/utane/internal/app"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "serve":
		return app.Serve()
	default:
		return usageError{}
	}
}

type usageError struct{}

func (usageError) Error() string {
	return "usage: go run . serve"
}
