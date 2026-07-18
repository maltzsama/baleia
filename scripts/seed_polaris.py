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

Usage:
    conda activate baleia
    ./scripts/seed_polaris.py
"""

import os
import sys
import requests
from pyiceberg.catalog import load_catalog
from pyiceberg.schema import Schema
from pyiceberg.types import LongType, StringType, NestedField

POLARIS_URL = os.getenv("POLARIS_URL", "http://localhost:8181")
REALM = os.getenv("POLARIS_REALM", "POLARIS")
CLIENT_ID = os.getenv("POLARIS_CLIENT_ID", "admin")
CLIENT_SECRET = os.getenv("POLARIS_CLIENT_SECRET", "admin")

CATALOG = os.getenv("CATALOG", "baleia")
NAMESPACE = os.getenv("NAMESPACE", "analytics")
TABLE = os.getenv("TABLE", "events")

S3_ENDPOINT = os.getenv("S3_ENDPOINT", "http://rustfs:9000")
BUCKET = os.getenv("BUCKET", "baleia")
BASE_LOCATION = f"s3://{BUCKET}/warehouse"

TOKEN_URL = f"{POLARIS_URL}/api/catalog/v1/oauth/tokens"
MANAGEMENT_URL = f"{POLARIS_URL}/api/management/v1/catalogs"
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


def seed_table():
    print(f"==> Creating namespace '{NAMESPACE}' and table '{TABLE}' via PyIceberg")
    catalog = load_catalog(
        "polaris_rest",
        **{
            "type": "rest",
            "uri": f"{POLARIS_URL}/api/catalog",
            "warehouse": BASE_LOCATION,
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
    catalog.create_table(f"{NAMESPACE}.{TABLE}", schema)
    print(f"    done: {CATALOG}.{NAMESPACE}.{TABLE}")


def main():
    token = get_token()
    drop_catalog(token)
    create_catalog(token)
    seed_table()
    print("==> Done")


if __name__ == "__main__":
    main()
