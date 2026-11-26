#!/usr/bin/env python3
"""Verify the deployed Product Manager through the normal SDK, using invented data."""
import argparse
import copy
import json
import math
import os
import re
import subprocess
import uuid
from pathlib import Path
from urllib.parse import urlencode

HERE = Path(__file__).resolve().parent
APP = HERE.parent
ROOT = APP.parents[1]
SYNTHETIC = "qa/synthetic/atlas"
CONTROL = "qa/synthetic/lumen"
TAG = "сгенерирована ИИ-помощником"


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def fixture(name):
    return json.loads((HERE / name).read_text(encoding="utf-8"))


class SDK:
    def __init__(self, args):
        self.air = args.air
        self.options = ["--agent", "product-manager", "--url", args.origin,
                        "--dir", str(APP), "--timeout", "40s", "--json"]

    def call(self, name, payload, reject=None):
        cmd = [str(self.air), "tools", "invoke", name, "--input",
               json.dumps(payload, ensure_ascii=False), *self.options]
        reply = subprocess.run(cmd, capture_output=True, text=True, timeout=49)
        try:
            envelope = json.loads(reply.stdout)
        except (ValueError, TypeError) as exc:
            raise RuntimeError(f"{name}: SDK did not return a JSON receipt (exit {reply.returncode})") from exc
        result = envelope.get("data", {}).get("result", {})
        error = envelope.get("error") or result.get("error") or result.get("isError") or result.get("is_error")
        failure = reply.returncode != 0 or envelope.get("status") == "failed" or bool(error)
        if reject is not None:
            diagnostic = json.dumps(envelope, ensure_ascii=False)
            # Airlock's user-facing API may redact the application reason and return the exact run ID.
            # Only that signed application rejection is acceptable; transport/authentication errors fail.
            redacted = re.search(r"application tool returned an execution error; inspect run [a-f0-9-]{36}", diagnostic)
            check(failure and (reject in diagnostic or redacted),
                  f"{name}: expected application rejection for {reject!r}; SDK status={envelope.get('status')}, detail={str(error)[:350]}")
            return None
        check(not failure and "output" in result, f"{name}: SDK status={envelope.get('status')}, detail={str(error)[:350]}")
        output = result["output"]
        return json.loads(output) if isinstance(output, str) else output


    def approve(self, identifier, version, confirmed=True, want=(303, 200)):
        form = urlencode({"id": identifier, "version": version, "confirmed": "yes" if confirmed else "no"})
        cmd = [str(self.air), "request", "POST", "/approve", "--data", form, "--header",
               "Content-Type: application/x-www-form-urlencoded", *self.options]
        reply = subprocess.run(cmd, capture_output=True, text=True, timeout=49)
        payload = json.loads(reply.stdout)
        status = payload.get("data", {}).get("statusCode")
        check(status in want, f"unexpected approval route status: {status}")


def register(sdk, document):
    evidence = {"document": json.dumps(document, ensure_ascii=False)}
    summary = sdk.call("import_evidence", evidence)
    repeated = sdk.call("import_evidence", {"document": json.dumps(document, indent=2, ensure_ascii=False)})
    rows = [row for batch in document["batches"] for row in batch["observations"]]
    check(summary == repeated, "same snapshot changed across imports")
    ref = summary["snapshot_id"]
    check(re.fullmatch(r"ev-[a-f0-9]{64}", ref) and summary["count"] == len(rows), "incorrect snapshot count or ID")
    saved = sdk.call("read_evidence", {"snapshot_id": ref, "ids": [r["id"] for r in rows], "limit": 100})
    found = {r["id"]: r for r in saved["rows"]}
    check(saved["snapshot_id"] == ref and saved["total_matched"] == len(rows)
          and len(found) == len(rows), "missing or duplicated observed rows")
    for row in rows:
        actual = found[row["id"]]
        check(actual["source_ref"] == row["source_ref"] and actual["status"] == row["status"] and
              actual["product"] == row["product_key"] and actual["period"] == row["period"] and
              actual["value"] == str(row["value"]), f"changed source observation {row['id']}")
    return ref


def hypothesis(identifier):
    return {
        "schema_version": "aifence.v1", "id": identifier, "title": "QA. Проверить путь продления Atlas",
        "product_key": SYNTHETIC, "signal_ids": ["fixture/earlier", "fixture/baseline"],
        "support_ids": ["fixture/interview"], "counter_ids": ["fixture/prices", "fixture/auditing"],
        "unknowns": ["Перешли ли клиенты к менеджерам?"],
        "proposed_action": "Сверить синтетические независимые заказы с обращениями к менеджерам и пройти путь продления.",
        "success_criterion": {"metric_key": "self_service_order_index", "baseline_from": "2025-01-06",
                              "baseline_to": "2025-02-02", "observation_days": 28,
                              "rule": "difference_in_relative_changes_pp >= 10", "control_product_key": CONTROL},
        "status": "proposed", "economic_effect": {"status": "unknown", "assumptions": []},
    }


def awaiting(assessment):
    check(assessment["status"] == "awaiting_measurement" and assessment.get("value") is None and
          bool(assessment["reason"]), "missing measurements must not produce an effect")


def run_lifecycle(sdk, initial, identifier):
    followup = fixture("synthetic-followup.json")
    for batch in followup["batches"]:
        if batch["source"] == "release":
            batch["observations"][0]["details"]["hypothesis_id"] = identifier
    h = hypothesis(identifier)
    key = {"hypothesis_id": identifier}
    save = {"hypothesis": h, "expected_version": 0, "evidence_snapshot_id": initial}
    bad = copy.deepcopy(save)
    bad["hypothesis"]["support_ids"] = ["fixture/absent"]
    sdk.call("save_hypothesis", bad, reject="отсутствуют наблюдения")
    first = sdk.call("save_hypothesis", save)
    check(first["version"] == 1 and first["hypothesis"] == h and first["evidence_snapshot_id"] == initial, "proposal mismatch")
    check(sdk.call("get_hypothesis", key) == first, "proposal was not persisted exactly")
    sdk.call("save_hypothesis", save, reject="версия")
    sdk.call("send_to_backlog", key, reject="ещё не создана")
    sdk.approve(identifier, first["version"], confirmed=False, want=(400,))
    check(sdk.call("get_hypothesis", key) == first, "declined send changed the record")
    awaiting(sdk.call("evaluate_hypothesis", key))
    sdk.approve(identifier, first["version"])
    task = sdk.call("send_to_backlog", key)
    sdk.approve(identifier, first["version"])
    retry = sdk.call("send_to_backlog", key)
    check(retry == task and task["hypothesis_id"] == identifier and task["tag"] == TAG and
          task["tracker"] == "local_prototype_backlog" and task["evidence_snapshot_id"] == initial and
          task["success_criterion"] == h["success_criterion"] and bool(task["url"]) and bool(task["id"]),
          "backlog receipt mismatch or duplicate")
    in_backlog = sdk.call("get_hypothesis", key)
    check(in_backlog["version"] == 2 and in_backlog["task"] == task and
          in_backlog["hypothesis"]["status"] == "in_backlog" and
          in_backlog["hypothesis"]["backlog_ref"] == {k: task[k] for k in ("id", "tag", "url")}, "task readback mismatch")
    follow = register(sdk, followup)
    release = {**key, "snapshot_id": follow, "release_observation_id": "fixture/fix"}
    sdk.call("evaluate_hypothesis", {**release, "release_observation_id": "fixture/missing"}, reject="наблюдение отсутствует")
    check(sdk.call("get_hypothesis", key) == in_backlog, "unknown release reference changed saved state")
    pending = sdk.call("evaluate_hypothesis", release)
    awaiting(pending)
    check(pending["release_id"] == "fixture/fix" and pending["released_on"] == "2025-02-05" and
          pending["release_evidence_ref"] == f"{follow}/fixture/fix", "release not recorded")
    quality = sdk.call("evaluate_hypothesis", {**release, "baseline_id": "fixture/tag-before", "current_id": "fixture/tag-after"})
    awaiting(quality)
    measure = {**release, "baseline_id": "fixture/baseline", "current_id": "fixture/partial",
               "control_baseline_id": "fixture/control-baseline", "control_current_id": "fixture/control-after"}
    awaiting(sdk.call("evaluate_hypothesis", measure))
    measure["current_id"] = "fixture/after"
    answer = sdk.call("evaluate_hypothesis", measure)
    refs = [f"{follow}/fixture/{v}" for v in ("baseline", "after", "control-baseline", "control-after")]
    # Atlas recovers from 80 to 100 (+25%); Lumen from 100 to 105 (+5%).
    check(answer["status"] == "supported" and math.isclose(answer["value"], 20.0) and
          answer["unit"] == "п.п." and answer["evidence_refs"] == refs, "incorrect controlled effect or source IDs")
    final = sdk.call("get_hypothesis", key)
    check(final["assessment"] == answer and final["task"] == task and final["hypothesis"]["release_id"] == "fixture/fix" and
          final["hypothesis"]["economic_effect"]["status"] == "unknown" and
          final["hypothesis"]["status"] == "supported", "final persisted result or economic claim mismatch")
    check(sdk.call("evaluate_hypothesis", measure) == answer and sdk.call("get_hypothesis", key) == final,
          "evaluation retry changed the result or version")
    return {"scenario": "synthetic", "hypothesis_id": identifier, "baseline_snapshot": initial,
            "followup_snapshot": follow, "task_id": task["id"], "version": final["version"], "effect_pp": answer["value"]}


def prepare_chat(sdk, initial=None):
    return {"hypothesis_id": None, "prompt": "Что сейчас требует внимания в продажах?"}


def check_chat(sdk, identifier, confirmed=False):
    r = sdk.call("get_hypothesis", {"hypothesis_id": identifier})
    h, task = r["hypothesis"], r.get("task")
    ids = set(h["signal_ids"] + h["support_ids"] + h["counter_ids"])
    rows = sdk.call("read_evidence", {"snapshot_id": r["evidence_snapshot_id"], "ids": sorted(ids), "limit": 100})["rows"]
    check(h["id"] == identifier and h["economic_effect"]["status"] == "unknown" and
          ids and ids == {row["id"] for row in rows}, "chat card missing traceable evidence")
    if confirmed:
        check(h["status"] == "in_backlog" and task and task["tag"] == TAG and
              task["tracker"] == "local_prototype_backlog" and task["hypothesis_id"] == identifier and
              task["id"] == h["backlog_ref"]["id"] and task["evidence_snapshot_id"] == r["evidence_snapshot_id"] and
              h["success_criterion"] == task["success_criterion"], "confirmed task failed saved-state verification")
    else:
        check(h["status"] == "proposed" and not task, "initial request created a task before confirmation")
    return {"hypothesis_id": identifier, "task_id": task["id"] if task else None,
            "evidence_ids": sorted(ids), "status": h["status"]}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--air", default=os.environ.get("AIR", str(ROOT / ".runtime/bin/market-air")))
    p.add_argument("--origin", default="http://localhost:42080")
    action = p.add_mutually_exclusive_group()
    action.add_argument("--prepare-chat", action="store_true")
    action.add_argument("--check-chat", metavar="HYPOTHESIS_ID")
    action.add_argument("--check-confirmed", metavar="HYPOTHESIS_ID")
    args = p.parse_args()
    sdk = SDK(args)
    if args.prepare_chat:
        result = prepare_chat(sdk)
    elif args.check_chat or args.check_confirmed:
        result = check_chat(sdk, args.check_chat or args.check_confirmed, bool(args.check_confirmed))
    else:
        first = register(sdk, fixture("synthetic-input.json"))
        result = run_lifecycle(sdk, first, "qa/synthetic/atlas/" + uuid.uuid4().hex[:12])
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
