// Command backend serves the platform API and optional Web static assets.
package main

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/app"
	"io"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "sync-templates" || os.Args[1] == "sync-catalog") {
		os.Exit(syncTemplates(os.Stderr))
	}
	os.Exit(run(os.Stderr))
}
func run(stderr io.Writer) int { return app.Run(stderr, version) }
