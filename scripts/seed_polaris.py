#!/usr/bin/env python3
"""
Seed local Polaris with a catalog, namespace, and table for Baleia dev.

Uses STS-enabled storage (Polaris vends temp S3 credentials to clients)
pointing to RustFS. The catalog REST API allows Trino + Baleia to test
full credential vending.

Requires the docker-compose stack up (polaris + rustfs). Safe to re-run
(drops existing catalog first).

Usage:
    conda activate baleia
    pip install requests
    ./scripts/seed_polaris.py
"""

import os
import sys
import requests

POLARIS_URL = os.getenv("POLARIS_URL", "http://localhost:8181")
REALM = os.getenv("POLARIS_REALM", "POLARIS")
CLIENT_ID = os.getenv("POLARIS_CLIENT_ID", "admin")
CLIENT_SECRET = os.getenv("POLARIS_CLIENT_SECRET", "admin")

CATALOG = os.getenv("CATALOG", "baleia")
NAMESPACE = os.getenv("NAMESPACE", "analytics")
TABLE = os.getenv("TABLE", "events")

S3_ENDPOINT = os.getenv("S3_ENDPOINT", "http://rustfs:9000")
S3_REGION = os.getenv("S3_REGION", "us-east-1")
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


def create_namespace(token: str):
    print(f"==> Creating namespace '{NAMESPACE}'")
    r = requests.post(
        f"{CATALOG_REST_URL}/namespaces",
        headers=realm_headers(token),
        json={"namespace": [NAMESPACE], "properties": {}},
    )
    if r.status_code == 409:
        print("    (already exists)")
        return
    r.raise_for_status()
    print(f"    HTTP {r.status_code}")


def create_table(token: str):
    print(f"==> Creating table '{TABLE}' (STS, metadata goes to RustFS via Polaris)")
    payload = {
        "name": TABLE,
        "schema": {
            "type": "struct",
            "fields": [
                {"id": 1, "name": "id", "type": "long", "required": True},
                {"id": 2, "name": "event_name", "type": "string", "required": False},
                {"id": 3, "name": "created_at", "type": "string", "required": False},
            ],
        },
    }
    r = requests.post(
        f"{CATALOG_REST_URL}/namespaces/{NAMESPACE}/tables",
        headers=realm_headers(token),
        json=payload,
    )
    if r.status_code == 409:
        print("    (already exists)")
        return
    r.raise_for_status()
    print(f"    HTTP {r.status_code}")


def main():
    token = get_token()
    drop_catalog(token)
    create_catalog(token)
    create_namespace(token)
    create_table(token)
    print(f"==> Done: {CATALOG}.{NAMESPACE}.{TABLE}")


if __name__ == "__main__":
    main()
