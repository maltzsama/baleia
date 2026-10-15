-- +goose Up
-- The catalog handlers and the binding API write catalog_bindings
-- themselves now (#80): the transitional triggers of 00024 go, and
-- source_ref is written with the row.
DROP TRIGGER IF EXISTS catalogs_upsert_managed_binding ON catalogs;
DROP TRIGGER IF EXISTS catalogs_set_source_ref ON catalogs;
DROP FUNCTION IF EXISTS catalogs_upsert_managed_binding();
DROP FUNCTION IF EXISTS catalogs_set_source_ref();

-- +goose Down
-- +goose StatementBegin
CREATE FUNCTION catalogs_set_source_ref() RETURNS trigger
LANGUAGE plpgsql AS $baleia$
BEGIN
    IF NEW.origin = 'source' THEN
        NEW.source_ref := NEW.warehouse;
    END IF;
    RETURN NEW;
END
$baleia$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION catalogs_upsert_managed_binding() RETURNS trigger
LANGUAGE plpgsql AS $baleia$
DECLARE
    eng UUID;
BEGIN
    IF NEW.origin <> 'source' THEN
        RETURN NULL;
    END IF;
    SELECT engine_id INTO eng FROM trino_clusters WHERE id = NEW.cluster_id;
    IF eng IS NULL THEN
        RETURN NULL;
    END IF;
    INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, connector, extra_props, mode, enabled)
    VALUES (NEW.id, eng, NEW.trino_catalog_name, NEW.connector, NEW.extra_props, 'managed', NEW.enabled)
    ON CONFLICT (catalog_id, engine_id) DO UPDATE SET
        engine_catalog_name = EXCLUDED.engine_catalog_name,
        connector           = EXCLUDED.connector,
        extra_props         = EXCLUDED.extra_props,
        enabled             = EXCLUDED.enabled,
        updated_at          = now();
    -- A move to another cluster leaves no binding behind on the old engine.
    DELETE FROM catalog_bindings
    WHERE catalog_id = NEW.id AND mode = 'managed' AND engine_id <> eng;
    RETURN NULL;
END
$baleia$;
-- +goose StatementEnd

CREATE TRIGGER catalogs_set_source_ref
    BEFORE INSERT OR UPDATE ON catalogs
    FOR EACH ROW EXECUTE FUNCTION catalogs_set_source_ref();

CREATE TRIGGER catalogs_upsert_managed_binding
    AFTER INSERT OR UPDATE ON catalogs
    FOR EACH ROW EXECUTE FUNCTION catalogs_upsert_managed_binding();
