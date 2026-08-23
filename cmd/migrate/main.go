package main

import (
	"log"
	"os"

	"github.com/lalyod/utane/internal/app"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "up" {
		log.Fatal("usage: migrate up")
	}

	if err := app.Migrate(); err != nil {
		log.Fatal(err)
	}
}
