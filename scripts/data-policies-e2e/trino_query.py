#!/usr/bin/env python3
"""Runs one statement on Trino as Baleia's admin login, impersonating a user.

    trino_query.py <trino-url> <ca-file> <password-file> <user> <sql>

Prints one JSON object: {"state": ..., "rows": [...], "error": ...}. It exits 0
whatever the statement's outcome; the caller judges it. Only the standard
library, so the e2e needs nothing installed.
"""

import base64
import json
import ssl
import sys
import urllib.request

url, ca_file, password_file, user, sql = sys.argv[1:6]
with open(password_file, encoding="utf-8") as f:
    password = f.read().strip()
context = ssl.create_default_context(cafile=ca_file)
# The dev certificate names the in-cluster Service, not localhost.
context.check_hostname = False
headers = {
    "Authorization": "Basic " + base64.b64encode(f"baleia_admin:{password}".encode()).decode(),
    "X-Trino-User": user,
    "X-Trino-Source": "baleia-e2e-data-policies",
    # The catalog guard's workspace scope (ADR 0018), as Baleia sends it for
    # a user's statement: without it a user identity reaches no catalog.
    "X-Trino-Extra-Credential": "baleia.catalogs=e2e_governed",
}


def call(request):
    with urllib.request.urlopen(request, context=context, timeout=300) as response:
        return json.load(response)


result = {"state": None, "rows": [], "error": None}
try:
    page = call(urllib.request.Request(url + "/v1/statement", data=sql.encode(), headers=headers))
    while True:
        result["rows"] += page.get("data") or []
        result["state"] = page.get("stats", {}).get("state")
        if page.get("error"):
            result["error"] = page["error"].get("message")
        next_uri = page.get("nextUri")
        if not next_uri:
            break
        page = call(urllib.request.Request(next_uri, headers=headers))
except Exception as exc:  # A dropped connection is a failed statement too.
    result["state"], result["error"] = "FAILED", f"{type(exc).__name__}: {exc}"
print(json.dumps(result))
