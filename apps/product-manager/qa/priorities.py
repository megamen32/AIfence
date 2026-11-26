"""Exercise a test draft through the authenticated form and ordinary MCP readback."""
import json
import subprocess
from urllib.parse import urlencode


def request(sdk, method, path, form=None):
    args = [str(sdk.air), "request", method, path]
    if form is not None:
        args.extend(["--header", "Content-Type: application/x-www-form-urlencoded", "--data", urlencode(form)])
    proc = subprocess.run(args + sdk.options, capture_output=True, text=True, timeout=49)
    data = json.loads(proc.stdout)["data"]
    return data["statusCode"], data.get("body", "")


def check_priorities(sdk, record):
    identifier = record["hypothesis"]["id"]
    version = record["version"]
    def lookup(group):
        return next((row for row in sdk.call("rank_hypotheses", {})[group] if row["hypothesis_id"] == identifier), None)
    assert lookup("needs_estimate") and not lookup("ranked"), "missing estimate must stay outside economic ordering"
    status, body = request(sdk, "GET", "/priorities")
    assert status == 200 and identifier in str(body), "authenticated priorities page does not include proposed test"
    form = {"id": identifier, "version": version, "money": "12000", "hours": "4", "basis": "QA synthetic weekly margin and test effort"}
    assert request(sdk, "POST", "/priorities", form)[0] in (200, 303), "form write failed"
    ranked = lookup("ranked")
    assert ranked and ranked["weekly_potential_rub_per_check_hour"] == 3000 and ranked["estimate"]["basis"] == form["basis"]
    assert request(sdk, "POST", "/priorities", dict(form, hours="5"))[0] == 409, "stale estimate accepted"
    current = sdk.call("get_hypothesis", {"hypothesis_id": identifier})
    same = sdk.call("set_review_estimate", {"hypothesis_id": identifier, "expected_version": current["version"], "estimate": current["planning"]})
    assert same["version"] == current["version"], "repeated estimate created spurious revision"
    return same
