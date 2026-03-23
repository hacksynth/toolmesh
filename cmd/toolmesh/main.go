package main

import (
	"context"
	"os"

	"toolmesh/internal/cli"
)

var version = "dev"

func main() {
	app := cli.NewAppWithVersion(os.Stdout, os.Stderr, version)
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
