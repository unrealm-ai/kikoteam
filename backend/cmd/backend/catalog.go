package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"log"
	"os"
	"time"
)

func syncTemplates(stderr io.Writer) int {
	logger := log.New(stderr, "", 0)
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		logger.Println(err)
		return 1
	}
	if os.Getenv("DATABASE_URL") == "" {
		logger.Println("DATABASE_URL is required")
		return 1
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		logger.Println("invalid database configuration")
		return 1
	}
	defer db.Close() //nolint:errcheck // Process exits immediately after the operation.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		logger.Println("database connection failed")
		return 1
	}
	config, err := skillasset.LoadConfig(os.Getenv)
	if err != nil {
		logger.Println("invalid asset storage")
		return 1
	}
	assets, err := skillasset.NewStore(ctx, config)
	if err != nil {
		logger.Println("asset storage unavailable")
		return 1
	}
	defer assets.Close() //nolint:errcheck // Process exits after syncing.
	if err = repository.SyncCatalog(ctx, db, catalog, assets); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			logger.Printf("catalog sync failed: SQLSTATE %s (%s)\n", pg.Code, pg.ConstraintName)
		} else {
			logger.Println(err)
		}
		return 1
	}
	logger.Printf("Official catalog revision %d synchronized (%d skills, %d plugins, %d templates)\n", catalog.Revision, len(catalog.Skills), len(catalog.Plugins), len(catalog.Entries))
	return 0
}
