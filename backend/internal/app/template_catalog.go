package app

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"log/slog"
	"time"
)

// A standalone Backend can start before Event Hub has migrated its database.
func startTemplateCatalog(parent context.Context, db *sql.DB, catalog service.TemplateCatalog, templates *service.TemplateService, log *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			attempt, stop := context.WithTimeout(ctx, time.Minute)
			err := repository.SyncCatalog(attempt, db, catalog, templates.Assets)
			stop()
			if err == nil {
				log.Info("Official catalog ready", "revision", catalog.Revision)
				for {
					if e := repository.ProvisionCatalogStarters(ctx, db, templates); e != nil && ctx.Err() == nil {
						log.Error("Provisioning starter employee failed", "error", e)
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(10 * time.Second):
					}
				}
			}
			if ctx.Err() != nil {
				return
			}
			var pg *pgconn.PgError
			var conn *pgconn.ConnectError
			waiting := errors.As(err, &conn) || errors.Is(err, context.DeadlineExceeded) || pgconn.SafeToRetry(err)
			if errors.As(err, &pg) {
				waiting = pg.Code == "42P01" || pg.Code == "42703" || pg.Code == "57P03" || pg.Code == "40001" || pg.Code == "40P01"
			}
			if !waiting {
				log.Error("Official template catalog sync failed; run backend sync-catalog for diagnostics", "revision", catalog.Revision)
				return
			}
			log.Warn("Waiting for database or template catalog migration", "revision", catalog.Revision)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return func() { cancel(); <-done }
}
