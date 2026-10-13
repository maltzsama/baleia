#!/usr/bin/env python3
"""
Seed local Polaris with a catalog, namespace, and table for Baleia dev.

Creates a catalog with STS enabled so Polaris vends temporary S3
credentials to clients (Trino, Baleia). Uses PyIceberg for catalog
operations (namespace + table) to exercise the full OAuth2 + STS flow
with Polaris's REST API, and raw requests for the Management API
(catalog create) which PyIceberg doesn't cover.

Requires the docker-compose stack up (polaris + rustfs). Safe to re-run
(drops existing catalog first).

Runs inside the compose network (see the "seed-polaris" service in
docker-compose.yaml) so it resolves "polaris"/"rustfs" the same way every
other service does, exactly like the existing "rustfs-init" service.

Usage:
    docker compose run --rm seed-polaris
"""

import os
import sys
import requests
from pyiceberg.catalog import load_catalog
from pyiceberg.schema import Schema
from pyiceberg.types import LongType, StringType, NestedField
import pyarrow as pa

POLARIS_URL = os.getenv("POLARIS_URL", "http://localhost:8181")
REALM = os.getenv("POLARIS_REALM", "POLARIS")
CLIENT_ID = os.getenv("POLARIS_CLIENT_ID", "admin")
CLIENT_SECRET = os.getenv("POLARIS_CLIENT_SECRET", "admin")

CATALOG = os.getenv("CATALOG", "baleia_polaris")
NAMESPACE = os.getenv("NAMESPACE", "analytics")
TABLE = os.getenv("TABLE", "events")

S3_ENDPOINT = os.getenv("S3_ENDPOINT", "http://rustfs:9000")
BUCKET = os.getenv("BUCKET", "baleia")
BASE_LOCATION = f"s3://{BUCKET}/warehouse"

TOKEN_URL = f"{POLARIS_URL}/api/catalog/v1/oauth/tokens"
MANAGEMENT_URL = f"{POLARIS_URL}/api/management/v1/catalogs"
PRINCIPAL_ROLES_URL = f"{POLARIS_URL}/api/management/v1/principal-roles"
CATALOG_REST_URL = f"{POLARIS_URL}/api/catalog/v1/{CATALOG}"


def realm_headers(token: str) -> dict:
    return {
        "Authorization": f"Bearer {token}",
        "Polaris-Realm": REALM,
        "Content-Type": "application/json",
    }


def get_token() -> str:
    print("==> Getting Polaris OAuth token")
    r = requests.post(
        TOKEN_URL,
        data={
            "grant_type": "client_credentials",
            "client_id": CLIENT_ID,
            "client_secret": CLIENT_SECRET,
            "scope": "PRINCIPAL_ROLE:ALL",
        },
        headers={"Polaris-Realm": REALM},
    )
    r.raise_for_status()
    return r.json()["access_token"]


def drop_catalog(token: str):
    print(f"==> Dropping existing catalog '{CATALOG}' (if any)")
    h = realm_headers(token)
    requests.delete(f"{CATALOG_REST_URL}/namespaces/{NAMESPACE}/tables/{TABLE}", headers=h)
    requests.delete(f"{CATALOG_REST_URL}/namespaces/{NAMESPACE}", headers=h)
    requests.delete(f"{MANAGEMENT_URL}/{CATALOG}", headers=h)


def create_catalog(token: str):
    print(f"==> Creating catalog '{CATALOG}' with STS enabled")
    payload = {
        "catalog": {
            "name": CATALOG,
            "type": "INTERNAL",
            "properties": {
                "default-base-location": BASE_LOCATION,
            },
            "storageConfigInfo": {
                "storageType": "S3",
                "allowedLocations": [BASE_LOCATION],
                "endpoint": S3_ENDPOINT,
                "endpointInternal": S3_ENDPOINT,
                "pathStyleAccess": True,
            },
        }
    }
    r = requests.post(MANAGEMENT_URL, headers=realm_headers(token), json=payload)
    if r.status_code == 409:
        print("    (already exists)")
        return
    r.raise_for_status()
    print(f"    HTTP {r.status_code}")


def grant_catalog_privileges(token: str):
    h = realm_headers(token)
    role_name = "catalog_admin"

    # Grant CATALOG_MANAGE_CONTENT to the default catalog_admin role
    privs = ["CATALOG_MANAGE_CONTENT", "TABLE_WRITE_DATA"]
    for priv in privs:
        print(f"==> Granting '{priv}' to catalog role '{role_name}'")
        r = requests.put(
            f"{MANAGEMENT_URL}/{CATALOG}/catalog-roles/{role_name}/grants",
            headers=h,
            json={"grant": {"privilege": priv, "type": "catalog"}},
        )
        if r.status_code == 201:
            print(f"    HTTP 201")
        elif r.status_code == 409:
            print(f"    already granted")
        else:
            print(f"    HTTP {r.status_code}: {r.text[:200]}")


def seed_table():
    print(f"==> Creating namespace '{NAMESPACE}' and table '{TABLE}' via PyIceberg")
    catalog = load_catalog(
        "polaris_rest",
        **{
            "type": "rest",
            "uri": f"{POLARIS_URL}/api/catalog",
            "warehouse": CATALOG,
            "credential": f"{CLIENT_ID}:{CLIENT_SECRET}",
            "scope": "PRINCIPAL_ROLE:ALL",
            "header.Polaris-Realm": REALM,
        },
    )
    catalog.create_namespace(NAMESPACE)
    schema = Schema(
        NestedField(field_id=1, name="id", field_type=LongType(), required=True),
        NestedField(field_id=2, name="event_name", field_type=StringType(), required=False),
        NestedField(field_id=3, name="created_at", field_type=StringType(), required=False),
    )
    table = catalog.create_table(f"{NAMESPACE}.{TABLE}", schema)
    print(f"    done: {CATALOG}.{NAMESPACE}.{TABLE}")

    print(f"==> Writing sample rows into '{NAMESPACE}.{TABLE}'")
    pa_schema = pa.schema([
        pa.field("id", pa.int64(), nullable=False),
        pa.field("event_name", pa.string(), nullable=True),
        pa.field("created_at", pa.string(), nullable=True),
    ])
    rows = pa.table(
        {
            "id": pa.array([1, 2, 3], type=pa.int64()),
            "event_name": pa.array(["click", "view", "purchase"], type=pa.string()),
            "created_at": pa.array(
                ["2026-07-18T00:00:00", "2026-07-18T00:01:00", "2026-07-18T00:02:00"],
                type=pa.string(),
            ),
        },
        schema=pa_schema,
    )
    table.append(rows)
    print(f"    inserted {rows.num_rows} rows")


def main():
    token = get_token()
    drop_catalog(token)
    create_catalog(token)
    grant_catalog_privileges(token)
    seed_table()
    print("==> Done")


if __name__ == "__main__":
    main()
