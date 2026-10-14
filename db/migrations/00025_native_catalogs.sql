-- +goose Up

-- Native catalogs (found inside an engine, #13) have no Trino projection, so
-- the Trino columns of catalogs become nullable. Source catalogs keep them:
-- the CHECK holds what NOT NULL held for every row before.
ALTER TABLE catalogs
    ALTER COLUMN trino_catalog_name DROP NOT NULL,
    ALTER COLUMN cluster_id DROP NOT NULL,
    ALTER COLUMN warehouse DROP NOT NULL,
    ALTER COLUMN connector DROP NOT NULL,
    ADD CONSTRAINT catalogs_source_has_trino_columns CHECK (
        origin = 'native'
        OR (trino_catalog_name IS NOT NULL AND cluster_id IS NOT NULL
            AND warehouse IS NOT NULL AND connector IS NOT NULL));

-- +goose Down

-- Native catalogs cannot exist without the Trino columns; their bindings go
-- with them (ON DELETE CASCADE).
DELETE FROM catalogs WHERE origin = 'native';

ALTER TABLE catalogs
    DROP CONSTRAINT IF EXISTS catalogs_source_has_trino_columns,
    ALTER COLUMN trino_catalog_name SET NOT NULL,
    ALTER COLUMN cluster_id SET NOT NULL,
    ALTER COLUMN warehouse SET NOT NULL,
    ALTER COLUMN connector SET NOT NULL;
