#!/usr/bin/env python3
"""Read or fire the synthetic Metrika cron via the loopback Airlock API."""
import argparse
import http.client
import json
import os
from pathlib import Path
import sys
import time
from uuid import UUID

ORIGIN = "http://localhost:42080"
CRON = "watchdog_daily"
HANDLER = "watchdog_run"


def sdk_token():
    base = (Path.home() / "Library/Application Support" if sys.platform == "darwin"
            else Path(os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config"))
    try:
        return json.loads((base / "airlock/credentials.json").read_text())["sessions"][ORIGIN]["accessToken"]
    except (OSError, ValueError, KeyError, TypeError):
        raise RuntimeError("Log in to the loopback Airlock with the SDK first.") from None


def request(conn, token, method, path):
    conn.request(method, "/api/v1" + path, headers={"Authorization": "Bearer " + token})
    response = conn.getresponse()
    body = response.read(1 << 20)
    if not 200 <= response.status < 300:
        raise RuntimeError(f"{method} {path} returned HTTP {response.status}")
    return json.loads(body)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--fire", action="store_true", help="Fire the synthetic schedule once")
    mode.add_argument("--job-id", type=UUID, help="Read an already accepted job without firing again")
    args = parser.parse_args()
    token = sdk_token()
    with_conn = http.client.HTTPConnection("localhost", 42080, timeout=10)
    try:
        def api(method, path):
            return request(with_conn, token, method, path)

        apps = api("GET", "/agents").get("agents", [])
        app = next((item for item in apps if item.get("slug") == "metrika-analyst"), None)
        if not app:
            raise RuntimeError("Metrika Analyst is not visible to the local SDK account.")
        app_id = str(UUID(app["id"]))
        schedules = api("GET", f"/agents/{app_id}/schedules").get("schedules", [])
        schedule = next((item for item in schedules if item.get("slug") == CRON), None)
        if not schedule:
            raise RuntimeError("watchdog_daily has not been deployed to the local target.")
        if not args.fire and args.job_id is None:
            print(f"{CRON}: declared")
            return
        job_id = (UUID(api("POST", f"/agents/{app_id}/schedules/{CRON}/fire")["jobId"])
                  if args.fire else args.job_id)
        print(f"job:{job_id}", flush=True)
        deadline = time.monotonic() + 25
        while True:
            job = api("GET", f"/jobs/{job_id}")["job"]
            if job.get("agentId") != app_id or job.get("handlerName") != HANDLER:
                raise RuntimeError("Job does not belong to the Metrika watchdog.")
            if job["status"] not in ("queued", "running"):
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("Job still pending. Read the same ID using --job-id.")
            time.sleep(1)
        if job["status"] != "succeeded":
            raise RuntimeError(f"Job is {job['status']}; inspect it in Airlock.")
        result = json.loads(job["outputJson"])
        flags = [decision["alert"] for decision in result["decisions"]]
        if job.get("cronSlug") == CRON and flags != [False, True, None]:
            raise RuntimeError("Expected false/true/null from the synthetic cron. Check watchdog_canary_enabled.")
        print(json.dumps({"status": result["status"], "alerts": flags, "next_job_id": result.get("next_job_id")}))
    finally:
        with_conn.close()


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, KeyError, ValueError) as exc:
        if isinstance(exc, RuntimeError):
            sys.exit(str(exc))
        sys.exit("Could not read the local Airlock job receipt.")
