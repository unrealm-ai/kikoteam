package postgres

import (
	"embed"
	"io/fs"
)

// Embedding the directory also supports the initial, intentionally empty SQL set.
//
//go:embed migrations
var migrations embed.FS

func Files() fs.FS {
	f, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	} // Compile-time embedded directory.
	return f
}
