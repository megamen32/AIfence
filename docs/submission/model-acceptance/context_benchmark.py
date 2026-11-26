#!/usr/bin/env python3
"""Isolated, synthetic long-context recall benchmark for a loopback Ollama API.

Only generated filler and random marker values are sent. Never points at a
shared or external endpoint unless the operator explicitly opts in.
"""
import argparse
import json
import os
import pathlib
import random
import re
import secrets
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

WORDS = (
    "apple garden river stone forest window water paper table light tree cloud green blue "
    "orange house book chair morning evening quiet silver winter summer autumn spring "
    "north south east west field flower road bridge island mountain valley meadow sand "
    "bird fish horse rabbit fox bear moon star sun rain snow wind lake grass leaf seed "
    "bread rice milk tea coffee salt sugar lemon grape pear peach plum cherry "
    "circle square triangle line point angle color shape wood glass metal cloth "
    "warm cool soft hard small large old new deep shallow clear dark bright smooth "
    "calm simple normal steady gentle open closed empty full near far "
    "walk stand sit rest read write listen watch turn move rise fall "
    "build find keep hold carry send bring take give make use see hear "
    "path gate shelf room hall wall floor roof door lamp clock bowl cup"
).split()

def prompt_for(words, seed):
    rng = random.Random(seed)
    keys = ("ALPHA", "BRAVO", "CHARLIE")
    markers = {k: "".join(rng.choice("ABCDEFGHJKLMNPQRSTUVWXYZ23456789") for _ in range(8)) for k in keys}
    positions = [max(1, int(words * frac)) for frac in (0.05, 0.50, 0.95)]
    parts = [
        f"Synthetic archive {seed}. Ignore filler words. Remember each distinct marker record.",
    ]
    for i in range(words):
        if i in positions:
            k = keys[positions.index(i)]
            parts.append(f"\nMARKER {k} = {markers[k]}\n")
        parts.append(rng.choice(WORDS))
    parts.append("\nReturn only a JSON object with the exact values of ALPHA, BRAVO, and CHARLIE from the three marker records. Do not invent or omit any value.")
    return " ".join(parts), markers

def read_json(url, path, payload=None, timeout=5):
    raw = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urllib.request.Request(url + path, data=raw, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        return json.load(response)

def memory_current():
    try:
        cgroup = next(x.split(":", 2)[2] for x in pathlib.Path("/proc/self/cgroup").read_text().splitlines() if x.startswith("0::"))
        return int((pathlib.Path("/sys/fs/cgroup") / cgroup.lstrip("/") / "memory.current").read_text())
    except (OSError, ValueError, StopIteration):
        return None

def sample(url, state, stop):
    while not stop.is_set():
        n = memory_current()
        if n is not None:
            state["cgroup_peak_bytes"] = max(state.get("cgroup_peak_bytes", 0), n)
        try:
            models = read_json(url, "/api/ps", timeout=2).get("models", [])
            for m in models:
                if m.get("name") == state["model"] or m.get("model") == state["model"]:
                    for k in ("size", "size_vram", "context_length"):
                        v = m.get(k)
                        if isinstance(v, int):
                            state[k] = max(state.get(k, 0), v)
        except (OSError, ValueError):
            pass
        stop.wait(1.5)

def run_case(url, model, ctx, words, seed, threads, timeout, force_cpu):
    text, expected = prompt_for(words, seed)
    payload = {"model": model, "messages": [{"role": "user", "content": text}],
               "stream": False, "think": False, "keep_alive": "60s",
               "options": {"num_ctx": ctx, "num_predict": 160,
                           "temperature": 0, "seed": seed % 2147483647,
                           "num_thread": threads}}
    if force_cpu:
        payload["options"]["num_gpu"] = 0
    r = {"context": ctx, "filler_words": words, "model": model, "synthetic": True,
         "configured_threads": threads, "cpu_only": force_cpu}
    state = {"model": model}
    stop = threading.Event()
    thread = threading.Thread(target=sample, args=(url, state, stop), daemon=True)
    thread.start()
    t0 = time.monotonic()
    try:
        d = read_json(url, "/api/chat", payload, timeout)
        r["http_ok"] = True
        r["done"] = d.get("done", False)
        r["done_reason"] = d.get("done_reason")
        r["prompt_tokens"] = d.get("prompt_eval_count")
        r["output_tokens"] = d.get("eval_count")
        r["prompt_eval_seconds"] = (d.get("prompt_eval_duration") or 0) / 1e9
        r["load_seconds"] = (d.get("load_duration") or 0) / 1e9
        if r["prompt_eval_seconds"] and isinstance(r["prompt_tokens"], int):
            r["prefill_tokens_per_second"] = round(r["prompt_tokens"] / r["prompt_eval_seconds"], 2)
        result = d.get("message", {}).get("content", "")
        try:
            start, end = result.index("{"), result.rindex("}") + 1
            parsed = json.loads(result[start:end])
        except (AttributeError, TypeError, ValueError):
            parsed = {}
        if not isinstance(parsed, dict):
            parsed = {}
        r["markers_recalled"] = {k: parsed.get(k) == v for k, v in expected.items()}
        r["marker_score"] = sum(r["markers_recalled"].values())
        r["recall_pass"] = r["marker_score"] == len(expected)
    except urllib.error.HTTPError as e:
        r.update(http_ok=False, error=f"HTTP {e.code}", recall_pass=False)
    except (OSError, ValueError, TypeError) as e:
        r.update(http_ok=False, error_class=type(e).__name__, recall_pass=False)
    finally:
        r["elapsed_seconds"] = round(time.monotonic() - t0, 3)
        stop.set()
        thread.join(timeout=4)
        r["memory"] = {k: v for k, v in state.items() if k != "model"}
    r["context_filled"] = isinstance(r.get("prompt_tokens"), int) and 0.88*ctx <= r["prompt_tokens"] <= ctx
    r["pass"] = bool(r.get("http_ok") and r.get("done") and r["context_filled"] and r["recall_pass"])
    return r

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base-url", required=True)
    ap.add_argument("--model", default="gemma4:latest")
    ap.add_argument("--contexts", nargs="+", type=int, default=[32768, 49152, 65536])
    ap.add_argument("--threads", type=int, default=16)
    ap.add_argument("--timeout", type=int, default=150)
    ap.add_argument("--budget-seconds", type=int, default=600)
    ap.add_argument("--cpu-only", action="store_true")
    ap.add_argument("--allocation-only", action="store_true", help="Short prompts: verify allocated context, not full-context recall")
    ap.add_argument("--allow-shared", action="store_true", help="Explicit opt-in for port 11434/11437")
    args = ap.parse_args()
    u = urllib.parse.urlsplit(args.base_url)
    if u.scheme != "http" or u.hostname not in ("127.0.0.1","localhost") or u.username or u.password or u.query or u.fragment or u.path not in ("","/"):
        ap.error("only a loopback Ollama HTTP origin is allowed")
    if u.port in (None,11434,11437) and not args.allow_shared:
        ap.error("refusing the shared Ollama/queue ports without explicit opt-in")
    if not 1 <= args.threads <= 24 or not 10 <= args.timeout <= 900 or not 20 <= args.budget_seconds <= 1800:
        ap.error("threads or timeout out of allowed range")
    if any(c < 4096 or c > 131072 for c in args.contexts) or args.contexts != sorted(set(args.contexts)):
        ap.error("contexts must be ascending unique integers in [4096, 131072]")
    url = args.base_url.rstrip("/")
    deadline = time.monotonic() + args.budget_seconds
    # Estimate the real tokenizer cost with a moderate pilot; each prompt gets
    # a unique prefix/marker set so a warm prefix cannot fake the result.
    ratio = 1.0
    results = []
    seed = secrets.randbelow(1000000000)
    if args.allocation_only:
        for i, ctx in enumerate(args.contexts, 1):
            remaining = int(deadline - time.monotonic())
            if remaining < 10:
                break
            r = run_case(url, args.model, ctx, 220, seed+i, args.threads,
                         min(args.timeout, remaining), args.cpu_only)
            try:
                m = next(x for x in read_json(url, "/api/ps", timeout=3).get("models", [])
                         if x.get("name") == args.model or x.get("model") == args.model)
                allocated = m.get("context_length")
            except (OSError, ValueError, StopIteration, TypeError):
                allocated = r.get("memory", {}).get("context_length")
            row = {"event": "allocation", "requested_context": ctx, "allocated_context": allocated,
                   "short_prompt_tokens": r.get("prompt_tokens"), "markers_recalled": r.get("marker_score"),
                   "http_ok": r.get("http_ok"), "done": r.get("done"), "elapsed_seconds": r.get("elapsed_seconds"),
                   "memory": r.get("memory"), "error": r.get("error") or r.get("error_class"),
                   "full_context_recall_test": False}
            row["allocation_pass"] = bool(row["http_ok"] and row["done"] and allocated == ctx)
            results.append(row)
            print(json.dumps(row), flush=True)
            if not row["allocation_pass"]:
                break
        print(json.dumps({"event":"summary","mode":"allocation_only","results":results}), flush=True)
        return 0 if len(results)==len(args.contexts) and all(r["allocation_pass"] for r in results) else 2
    pilot = run_case(url, args.model, 4096, 3500, seed, args.threads, args.timeout, args.cpu_only)
    print(json.dumps({"event": "calibration", **pilot}), flush=True)
    if not pilot.get("http_ok") or not isinstance(pilot.get("prompt_tokens"), int) or not pilot["prompt_tokens"]:
        print(json.dumps({"event":"summary","reason":"calibration_failed","results":[]}), flush=True)
        return 2
    ratio = max(0.5, (pilot["prompt_tokens"] - 130) / 3500)
    for i, ctx in enumerate(args.contexts, 1):
        remaining = int(deadline - time.monotonic())
        if remaining < 20:
            break
        words = max(100, int((0.94 * ctx - 130) / ratio))
        r = run_case(url, args.model, ctx, words, seed+i, args.threads,
                     min(args.timeout, remaining), args.cpu_only)
        results.append(r)
        print(json.dumps({"event":"case_complete",**r}), flush=True)
        if not r.get("http_ok"):
            break
        if isinstance(r.get("prompt_tokens"), int) and r["prompt_tokens"] > 130:
            ratio = (r["prompt_tokens"]-130)/words
    print(json.dumps({"event":"summary","results":results,
                      "largest_recall_pass_context":max((r["context"] for r in results if r["pass"]), default=None),
                      "largest_observed_prompt_tokens":max((r.get("prompt_tokens") or 0 for r in results),default=0)}), flush=True)
    return 0 if len(results)==len(args.contexts) and all(r["pass"] for r in results) else 2

if __name__ == "__main__":
    raise SystemExit(main())
