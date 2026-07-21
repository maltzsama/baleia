-- Seed para os testes T1-T9 do plugin baleia-trino-catalog-store (CR-014 rev.3).
-- Idempotente: seguro rodar múltiplas vezes. Não é migration — rode manualmente
-- após o compose-up quando for validar a integração com o plugin.
--
-- Uso:
--   make seed-tpch
-- ou:
--   docker compose exec -T postgres psql -U baleia -d baleia < db/seed/tpch_teste.sql
--
-- O conector `tpch` é embutido no Trino e não precisa de nenhum serviço externo,
-- por isso é o conector usado para validar que o plugin lê a registry no boot.

INSERT INTO trino_catalog_registry
    (cluster_id, catalog_name, connector_name, properties, enabled, sync_status, updated_by)
VALUES (
    (SELECT id FROM trino_clusters WHERE name = 'default'),
    'tpch_teste',
    'tpch',
    '{"tpch.splits-per-node": "4"}'::jsonb,
    TRUE,
    'pending',
    'baleia'
)
ON CONFLICT (cluster_id, catalog_name) DO NOTHING;