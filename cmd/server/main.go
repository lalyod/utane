package main

import (
	"log"

	"github.com/lalyod/mysong/internal/app"
)

func main() {
	if err := app.Serve(); err != nil {
		log.Fatal(err)
	}
}
