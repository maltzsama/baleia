// Command backfill-references extracts the tables and equi-joins of finished
// statements and stores them (B0, #530, #538). It is a one-off job: it walks
// history in keyset order and is idempotent, because Persist replaces a
// statement's rows, so it can be re-run safely.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/sqlref"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	url := flag.String("database-url", "", "Postgres URL (default $DATABASE_URL or $BALEIA_DATABASE_URL)")
	batch := flag.Int("batch", 500, "statements per page")
	flag.Parse()
	if *url == "" {
		*url = os.Getenv("DATABASE_URL")
	}
	if *url == "" {
		*url = os.Getenv("BALEIA_DATABASE_URL")
	}
	if *url == "" {
		log.Fatal("backfill-references: set --database-url, DATABASE_URL or BALEIA_DATABASE_URL")
	}
	if *batch < 1 {
		log.Fatal("backfill-references: --batch must be positive")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *url)
	if err != nil {
		log.Fatalf("backfill-references: connect: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	var cursor pgtype.UUID
	total := 0
	for {
		rows, err := q.ListStatementsForReferenceBackfill(ctx, db.ListStatementsForReferenceBackfillParams{
			Cursor: cursor,
			Lim:    int32(*batch), //nolint:gosec // --batch is validated positive
		})
		if err != nil {
			log.Fatalf("backfill-references: list: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := sqlref.Persist(ctx, q, row.ID, row.WorkspaceID, row.TargetCatalogID, row.TargetSchema, row.Sql); err != nil {
				log.Printf("backfill-references: statement %s: %v", uuid.UUID(row.ID.Bytes).String(), err)
			}
			cursor = row.ID
			total++
		}
		log.Printf("backfill-references: processed %d statements", total)
	}
	log.Printf("backfill-references: done, %d statements", total)
}
