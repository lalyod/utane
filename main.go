package main

import (
	"log"
	"os"

	"github.com/lalyod/mysong/internal/app"
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
	case len(args) == 2 && args[0] == "db" && args[1] == "migrate":
		return app.Migrate()
	default:
		return usageError{}
	}
}

type usageError struct{}

func (usageError) Error() string {
	return "usage: go run . serve | go run . db migrate"
}
