#!/usr/bin/env python3
"""Recompute, import and open a local investigation using the signed Airlock SDK."""
import argparse
import json
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlencode

APP = Path(__file__).resolve().parent
ROOT = APP.parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--materials", type=Path, default=ROOT / "материалы" / "Материалы заказчика")
    parser.add_argument("--output", type=Path, default=ROOT / ".runtime" / "product-manager" / "evidence.json")
    parser.add_argument("--air", type=Path, default=ROOT / ".runtime" / "bin" / "market-air")
    args = parser.parse_args()
    subprocess.run([sys.executable, str(APP / "research" / "import_case.py"), "--materials",
                    str(args.materials), "--output", str(args.output)], check=True)
    def request(path, content_type, body):
        call = subprocess.run([str(args.air),"request","POST",path,"--header",
                               "Content-Type: "+content_type,"--header","Accept: application/json",
                               "--data",body,"--url","http://localhost:42080","--agent","product-manager","--json"],
                              cwd=APP, capture_output=True, text=True, timeout=70)
        receipt = json.loads(call.stdout)
        if call.returncode or receipt.get("data",{}).get("statusCode") != 200:
            raise RuntimeError("Airlock rejected "+path+"; check deployment and active CLI login")
        return json.loads(receipt["data"]["body"])
    snapshot = request("/import","application/json",args.output.read_text(encoding="utf-8"))
    record = request("/investigate","application/x-www-form-urlencoded",urlencode({"snapshot_id":snapshot["snapshot_id"]}))
    print(json.dumps({"snapshot_id":snapshot["snapshot_id"], "observations":snapshot["count"],
                      "hypothesis_id":record["hypothesis"]["id"],"version":record["version"],
                      "status":record["hypothesis"]["status"],"review_updated_evidence":snapshot["snapshot_id"] != record["evidence_snapshot_id"],
                      "evidence_snapshot_id":record["evidence_snapshot_id"]},ensure_ascii=False))


if __name__ == "__main__":
    main()
