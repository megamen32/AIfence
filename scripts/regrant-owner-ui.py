#!/usr/bin/env python3
"""Ensure the owner keeps authenticated UI access after an app deployment.

The Airlock app-members API is the same write path used by the Access panel.
The operation is idempotent and never prints credentials or response bodies.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


OWNER_EMAIL = "megamen932@gmail.com"
APPS = {
    "competitor-research": "03dc1bb2-869e-4783-897f-7c7fbc4b395d",
    "web-scraper": "cdd2224a-e2e8-4be8-a325-49fe7fa37be7",
    "google-trends": "865ff815-30fb-4a03-983e-300501230909",
    "wordstat": "31d1d3e9-2488-47ca-ab01-ddd50afe8882",
    "metrika-analyst": "6e985f49-ae84-4f6d-b345-edb4ea0d95cf",
    "market-watch": "8702e3ad-c528-45af-8172-00e3be59ae7f",
    "product-manager": "01a5630d-261e-4c41-88a7-15fe5bd4fa2b",
}


def credentials_path() -> Path:
    override = os.environ.get("AIRLOCK_CREDENTIALS_FILE")
    if override:
        return Path(override).expanduser()
    if platform.system() == "Darwin":
        return Path.home() / "Library/Application Support/airlock/credentials.json"
    config_home = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
    return config_home / "airlock/credentials.json"


def origin(raw: str) -> str:
    parsed = urllib.parse.urlsplit(raw)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ValueError("AIRLOCK_URL must be an HTTP(S) origin")
    if parsed.username or parsed.password or parsed.path not in {"", "/"} or parsed.query or parsed.fragment:
        raise ValueError("AIRLOCK_URL must be an origin without credentials or a path")
    host = parsed.hostname.lower()
    port = parsed.port
    if port is None or port == (443 if parsed.scheme == "https" else 80):
        return f"{parsed.scheme.lower()}://{host}"
    return f"{parsed.scheme.lower()}://{host}:{port}"


def request(base: str, token: str, method: str, path: str, body: dict | None = None) -> dict:
    payload = None if body is None else json.dumps(body).encode("utf-8")
    headers = {"Accept": "application/json", "Authorization": f"Bearer {token}"}
    if payload is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base + path, data=payload, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as response:
            raw = response.read()
    except urllib.error.HTTPError as exc:
        if exc.code in {401, 403}:
            raise RuntimeError(f"Airlock owner grant API returned HTTP {exc.code}") from None
        raise RuntimeError(f"Airlock owner grant API returned HTTP {exc.code}") from None
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError("Airlock owner grant API returned invalid JSON") from exc
    if not isinstance(value, dict):
        raise RuntimeError("Airlock owner grant API returned an unexpected response")
    return value


def load_token(base: str) -> str:
    path = credentials_path()
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise RuntimeError(f"Airlock credentials are unavailable at {path}") from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError("Airlock credentials are not valid JSON") from exc
    session = document.get("sessions", {}).get(base, {})
    token = session.get("accessToken")
    if not token:
        raise RuntimeError(f"No Airlock CLI login is stored for {base}")
    return token


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default=os.environ.get("AIRLOCK_URL", "http://localhost:42080"))
    args = parser.parse_args()
    try:
        base = origin(args.url)
        token = load_token(base)
        me = request(base, token, "GET", "/api/v1/me").get("user", {})
        if me.get("email") != OWNER_EMAIL:
            raise RuntimeError("Airlock CLI login is not the configured owner account")
        owner_id = me.get("id")
        if not owner_id:
            raise RuntimeError("Airlock owner response did not include a user id")

        changed = []
        verified = []
        for slug, app_id in APPS.items():
            members = request(base, token, "GET", f"/api/v1/agents/{app_id}/members").get("members", [])
            member = next(
                (item for item in members if item.get("userId") == owner_id or item.get("email") == OWNER_EMAIL),
                None,
            )
            if member and member.get("role") in {"admin", "user"}:
                verified.append(slug)
                continue
            request(base, token, "POST", f"/api/v1/agents/{app_id}/members", {"userId": owner_id, "role": "admin"})
            changed.append(slug)
        print(f"owner UI grants verified={len(verified)} restored={len(changed)}")
        if changed:
            print("restored apps=" + ",".join(changed))
        return 0
    except (RuntimeError, ValueError) as exc:
        print(f"owner UI grant check failed: {exc}", file=sys.stderr)
        return 75


if __name__ == "__main__":
    raise SystemExit(main())
