#!/usr/bin/env python3
"""Configures a fresh Atlas server for the M365 development environment.

Run once by atlas-setup.sh with the JSON file OpenTofu wrote (/etc/atlas/bootstrap.json):
  - signs in as the seeded administrator,
  - stores each worker's credential bundle in the vault (PUT /api/v1/secrets/{name}),
  - creates the Console worker that references it (POST /api/v1/configured-workers),
    unless a worker of that name exists already,
  - mints an API token sealed in the vault (POST /api/v1/api-tokens), unless one of that
    name exists already.
Atlas then supervises the workers itself. On success the settings file, which holds
the credentials, is deleted: from then on they live only in Atlas's vault.

Standard library only; never prints a secret.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def log(msg):
    print(f"atlas-bootstrap: {msg}", flush=True)


class Atlas:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.cookie = None

    def call(self, method, path, body=None, ok=(200, 201, 204)):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Accept", "application/json")
        req.add_header("Origin", self.base)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                status, headers, raw = resp.status, resp.headers, resp.read()
        except urllib.error.HTTPError as e:
            status, headers, raw = e.code, e.headers, e.read()
        if status not in ok:
            # The server's error text names the problem and never echoes a secret back.
            raise RuntimeError(f"{method} {path}: HTTP {status}: {raw[:400].decode(errors='replace')}")
        try:
            body = json.loads(raw) if raw.strip() else None
        except ValueError:
            body = raw.decode(errors="replace")  # /healthz answers plain text
        return status, headers, body

    def wait_healthy(self, minutes=15):
        deadline = time.time() + minutes * 60
        while True:
            try:
                self.call("GET", "/healthz")
                return
            except Exception as e:  # not up yet
                if time.time() > deadline:
                    raise RuntimeError(f"Atlas did not become healthy: {e}")
                time.sleep(5)

    def login(self, username, password):
        for attempt in range(10):
            try:
                _, headers, _ = self.call("POST", "/api/v1/auth/login",
                                          {"username": username, "password": password})
                break
            except RuntimeError as e:
                if attempt == 9:
                    raise
                log(f"login not possible yet ({e}); retrying")
                time.sleep(10)
        cookies = [c.split(";", 1)[0] for c in headers.get_all("Set-Cookie") or []]
        if not cookies:
            raise RuntimeError("login answered without a session cookie")
        self.cookie = "; ".join(cookies)


def names_of(listing):
    items = listing if isinstance(listing, list) else (listing or {}).get("workers") or (listing or {}).get("tokens") or []
    return {str(i.get("name", "")).lower() for i in items if isinstance(i, dict)}


def main(path):
    with open(path, encoding="utf-8") as f:
        cfg = json.load(f)
    atlas = Atlas(cfg["baseUrl"])
    log("waiting for Atlas")
    atlas.wait_healthy()
    atlas.login(cfg["adminUsername"], cfg["adminPassword"])
    log("signed in as administrator")

    _, _, listing = atlas.call("GET", "/api/v1/configured-workers")
    existing = names_of(listing)
    for w in cfg["workers"]:
        secret_name = w["secretName"]
        atlas.call("PUT", "/api/v1/secrets/" + urllib.parse.quote(secret_name, safe=""), {"value": w["secret"]})
        if w["name"].lower() in existing:
            log(f"worker {w['name']} exists; its vault bundle {secret_name} was refreshed")
            continue
        body = {"name": w["name"], "kind": w["kind"], "credentialsRef": secret_name}
        if w["kind"] == "entra":
            # Directory fields in forms search through this Worker (ADR-0473); its owner
            # opens it to them, and nothing else does (examples/m365-dienste).
            body["formLookups"] = True
        for field in ("endpoint", "provider", "sender"):
            if w.get(field):
                body[field] = w[field]
        atlas.call("POST", "/api/v1/configured-workers", body)
        log(f"worker {w['name']} ({w['kind']}) created")

    token = cfg.get("apiToken")
    if token:
        _, _, tokens = atlas.call("GET", "/api/v1/api-tokens")
        if token["name"].lower() in names_of(tokens):
            log(f"API token '{token['name']}' exists")
        else:
            atlas.call("POST", "/api/v1/api-tokens", token)
            log(f"API token '{token['name']}' minted and sealed in the vault as '{token['vaultSecret']}'")

    os.remove(path)
    log("done; the settings file with the credentials was removed")


if __name__ == "__main__":
    try:
        main(sys.argv[1] if len(sys.argv) > 1 else "/etc/atlas/bootstrap.json")
    except Exception as e:
        log(f"FAILED: {e}")
        sys.exit(1)
