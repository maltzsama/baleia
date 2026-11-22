---
sidebar_label: Apache Kafka
description: "Connect Apache Kafka to Baleia. Apache Kafka topics, read as tables through Trino. See its settings and what Baleia does with it."
---

# Connect Apache Kafka to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Apache Kafka topics, read as tables through Trino.

## What Baleia does with it

- Sets up and keeps the matching Trino catalog (type `kafka`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `nodes` | text | yes |  | Comma-separated host:port list, e.g. kafka-0:9092,kafka-1:9092. Every Trino node must reach every broker. |
| `security_protocol` | select |  | `PLAINTEXT` | one of `PLAINTEXT`, `SSL`. SSL checks the brokers against the Trino JVM's default trust store. |

### Catalog fields

Each catalog of the connection takes these values.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `topics` | text | yes |  | Comma-separated topics to read as tables. Each topic is a table of the connector's internal columns (_key, _message, _timestamp, …). |
| `schema` | text |  | `default` | Schema the topics are listed under. |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
