---
sidebar_position: 2
---

# Quick start

Run Baleia on your machine with Docker, connect the bundled Polaris catalog and run a first query. It takes a few minutes, most of them pulling images.

## Start the stack

You need Docker with Compose and `make`.

```bash
git clone https://github.com/maltzsama/baleia.git
cd baleia
make up
```

`make up` starts Baleia (API, worker and migrations) with everything it needs:

- **PostgreSQL** for Baleia's own data.
- **Temporal** for long-running work.
- **RustFS** as S3-compatible storage for query results and the lake.
- **Apache Polaris** as an Iceberg catalog, seeded with the catalog `baleia_polaris` and the table `analytics.events`.
- **Trino** with the [catalog-store plugin](https://github.com/maltzsama/baleia-trino-catalog-store).
- **Mailpit** to catch e-mails.

Optional profiles add more sources:
- `docker compose --profile sources up -d` adds MySQL, MariaDB, SQL Server, Oracle, ClickHouse, Lakekeeper, Hive Metastore, Unity and a mock AWS.
- `--profile ldap` adds a directory for [LDAP sign-in](ldap.md).

| Service | Address |
|---|---|
| Baleia | http://localhost:8080 |
| Temporal UI | http://localhost:8233 |
| Polaris console | http://localhost:4000 |
| Mailpit (e-mails) | http://localhost:8025 |

## Create the admin account

Open http://localhost:8080. On the first visit a setup wizard creates the admin account; after that you sign in normally.

The stack registers the bundled Trino as the engine `trino-default` on start, from `TRINO_URL`.

## Connect the bundled catalog

In **Connections**, click **New connection**. The wizard walks through the steps:

1. Pick **Apache Polaris** and fill in:
   - URL `http://polaris:8181`, as the containers reach it;
   - client ID `admin` and client secret `admin`.
2. **Test connection**.
3. **Add catalog** with the warehouse `baleia_polaris`. Baleia creates the matching catalog in Trino: that is a [binding](concepts.md#bindings).
4. **Grant access** to your workspace, so it can query the catalog.

The connection types and their fields are listed in [Connections](connections.md).

## Run a query

Open **SQL**, pick the catalog in the target selector and run:

```sql
SELECT * FROM analytics.events LIMIT 10;
```

Run with **⌘/Ctrl + Enter**. The [workbench guide](guides/workbench.md) covers scripts, results, exports and saved queries.

## Stop

```bash
make down      # stop, keeping the data
make down-v    # stop and delete the volumes
```

## Next

- [Install on Kubernetes](install.md) for a real deployment.
- [Concepts](concepts.md) for how connections, engines, catalogs and bindings fit together.
- [Development](development.md) to run Baleia from source.
