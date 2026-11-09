package sqlref

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the persistence of a statement's references and joins; *repo.Repo
// and *repo.Repo implement it.
type Store interface {
	DeleteStatementReferences(ctx context.Context, statementID pgtype.UUID) error
	DeleteStatementJoins(ctx context.Context, statementID pgtype.UUID) error
	InsertStatementReference(ctx context.Context, arg db.InsertStatementReferenceParams) error
	InsertStatementJoin(ctx context.Context, arg db.InsertStatementJoinParams) error
}

// Persist replaces the stored references and joins of one statement with the
// extractor's result (#538). It is idempotent: the old rows are deleted first.
// The caller owns the transaction.
func Persist(ctx context.Context, q Store, statementID, workspaceID, catalogID pgtype.UUID, namespace, sql string) error {
	ref := Extract(sql)
	if err := q.DeleteStatementReferences(ctx, statementID); err != nil {
		return err
	}
	if err := q.DeleteStatementJoins(ctx, statementID); err != nil {
		return err
	}
	for _, table := range ref.Tables {
		if len(table.Path) == 0 {
			continue
		}
		ns := namespace
		if len(table.Path) >= 2 {
			ns = table.Path[len(table.Path)-2]
		}
		if err := q.InsertStatementReference(ctx, db.InsertStatementReferenceParams{
			StatementID: statementID,
			WorkspaceID: workspaceID,
			CatalogID:   catalogID,
			Namespace:   ns,
			TableName:   table.Path[len(table.Path)-1],
			Alias:       table.Alias,
			Kind:        table.Kind,
		}); err != nil {
			return err
		}
	}
	for _, join := range ref.Joins {
		if err := q.InsertStatementJoin(ctx, db.InsertStatementJoinParams{
			StatementID: statementID,
			WorkspaceID: workspaceID,
			LeftAlias:   join.LeftAlias,
			LeftColumn:  join.LeftColumn,
			RightAlias:  join.RightAlias,
			RightColumn: join.RightColumn,
		}); err != nil {
			return err
		}
	}
	return nil
}
