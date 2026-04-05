# 🐋 Baleia

UI de primeira classe para Trino + plataforma de gerenciamento de catálogo Apache Iceberg.

## Stack

- **Frontend:** TypeScript + Svelte 5 + Monaco Editor
- **Backend:** Go (Chi, nhooyr/websocket, sqlc)
- **Orquestração:** Temporal.io
- **Banco:** CockroachDB
- **Catálogo:** Apache Polaris
- **Query engine:** Trino

## Dev

```bash
# Subir infraestrutura local
make compose-up

# Rodar API
make dev

# Rodar worker Temporal
make worker

# Gerar código sqlc
make sqlc
```
