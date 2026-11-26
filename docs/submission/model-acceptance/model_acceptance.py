#!/usr/bin/env python3
"""Synthetic OpenAI-compatible model acceptance. Never sends customer data."""
import argparse
import base64
import json
import os
import struct
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib

MODEL_CASES = ("text", "stream", "tool", "vision", "evidence")


def tiny_png():
    """A deterministic 64x64 solid red PNG, no dependencies."""
    def chunk(name, data):
        body = name + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body))
    raw = b"".join(b"\x00" + b"\xff\x00\x00" * 64 for _ in range(64))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 64, 64, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


def payload(case, model):
    body = {"model": model, "stream": case == "stream", "temperature": 0,
            "max_tokens": 96, "messages": [{"role": "user", "content": "Reply with exactly OK. Synthetic test."}]}
    if case == "stream":
        # Airlock requires useful streaming output, not just HTTP 200.
        body["stream_options"] = {"include_usage": True}
    if case == "tool":
        body["max_tokens"] = 256
        body["messages"] = [{"role": "user", "content":
                             "Use the record_status tool once with status ready. Do not answer with prose."}]
        body["tools"] = [{"type": "function", "function": {
            "name": "record_status", "description": "Record a synthetic readiness result",
            "parameters": {"type": "object", "properties": {"status": {"type": "string", "enum": ["ready"]}},
                           "required": ["status"], "additionalProperties": False}}}]
        body["tool_choice"] = "auto"
    if case == "vision":
        body["max_tokens"] = 512
        image = "data:image/png;base64," + base64.b64encode(tiny_png()).decode("ascii")
        body["messages"] = [{"role": "user", "content": [
            {"type": "text", "text": "What is the color of this solid square? Reply exactly RED or BLUE."},
            {"type": "image_url", "image_url": {"url": image}}]}]
    if case == "evidence":
        body["max_tokens"] = 512
        body["messages"] = [
            {"role": "system", "content": "You are reviewing synthetic product evidence. Return one JSON object with keys cause_status, cart_event, next_action, backlog_action. Use the offered enum values."},
            {"role": "user", "content": "Synthetic product Atlas: orders index fell 100 to 70; control Lumen remained 100. Release Z9 happened near the decline. Historic add_to_cart is missing. Today desktop and mobile show the item in cart and the browser sends add. No human authorization to send backlog. Choose cause_status from proven/unproven; cart_event from zero/missing; next_action from blame_release/verify_historical_tracking_and_release/ignore; backlog_action from send/draft. JSON only."}
        ]
    return body


def safe_usage(value):
    if not isinstance(value, dict):
        return None
    fields = {k: value[k] for k in ("prompt_tokens", "completion_tokens", "total_tokens")
              if type(value.get(k)) is int and value[k] >= 0}
    return fields or None


def parse_stream(lines):
    text = []
    usage = None
    finished = False
    model = None
    for raw in lines:
        if isinstance(raw, bytes):
            raw = raw.decode("utf-8", "replace")
        raw = raw.strip()
        if not raw.startswith("data:"):
            continue
        data = raw[5:].strip()
        if data == "[DONE]":
            finished = True
            break
        try:
            chunk = json.loads(data)
        except ValueError:
            continue
        if chunk.get("error"):
            raise ValueError("stream_error")
        if isinstance(chunk.get("model"), str):
            model = chunk["model"]
        usage = safe_usage(chunk.get("usage")) or usage
        for choice in chunk.get("choices", [])[:1]:
            part = choice.get("delta", {}).get("content")
            if isinstance(part, str):
                text.append(part)
            if choice.get("finish_reason"):
                finished = True
    return {"content": "".join(text), "finished": finished, "model": model, "usage": usage}


def evaluate(case, data):
    if case == "stream":
        ok = data.get("finished") and data.get("content", "").strip() == "OK"
        return bool(ok), "ok" if ok else "incomplete_or_wrong_stream"
    choices = data.get("choices") or []
    if not choices or not isinstance(choices[0], dict):
        return False, "missing_choice"
    first = choices[0]
    message = first.get("message") or {}
    if case == "tool":
        calls = message.get("tool_calls") or []
        if len(calls) != 1:
            return False, "missing_or_multiple_tool_calls"
        fn = calls[0].get("function") or {}
        try:
            arguments = fn.get("arguments")
            if isinstance(arguments, str):
                arguments = json.loads(arguments)
        except (ValueError, TypeError):
            return False, "invalid_tool_arguments"
        ok = fn.get("name") == "record_status" and arguments == {"status": "ready"}
        return ok, "ok" if ok else "wrong_tool_or_arguments"
    content = message.get("content")
    if case == "evidence":
        try:
            if not isinstance(content, str):
                raise ValueError("missing content")
            # A presentation code fence is harmless; validate the object, not prose.
            d = json.loads(content[content.index("{"):content.rindex("}") + 1])
        except (ValueError, TypeError):
            return False, "invalid_evidence_response"
        expected = {"cause_status": "unproven", "cart_event": "missing",
                    "next_action": "verify_historical_tracking_and_release", "backlog_action": "draft"}
        return (True, "ok") if d == expected else (False, "wrong_evidence_response")
    wanted = "RED" if case == "vision" else "OK"
    ok = isinstance(content, str) and content.strip().upper() == wanted
    return ok, "ok" if ok else "wrong_or_empty_answer"


def endpoint(base):
    u = urllib.parse.urlsplit(base)
    if u.scheme not in ("http", "https") or not u.hostname or u.username or u.password or u.query or u.fragment:
        raise ValueError("base URL must be HTTP(S) without credentials, query or fragment")
    return base.rstrip("/") + "/chat/completions"


def run_case(url, model, case, timeout, api_key=None):
    started = time.monotonic()
    result = {"case": case, "pass": False, "usage": None}
    request = urllib.request.Request(url, data=json.dumps(payload(case, model)).encode(),
                                     headers={"Content-Type": "application/json",
                                              **({"Authorization": "Bearer " + api_key} if api_key else {})},
                                     method="POST")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            result["http_status"] = response.status
            data = parse_stream(response) if case == "stream" else json.loads(response.read())
            result["pass"], result["reason"] = evaluate(case, data)
            result["usage"] = safe_usage(data.get("usage"))
            if case != "stream":
                choices = data.get("choices") or []
                if choices and isinstance(choices[0], dict):
                    finish = choices[0].get("finish_reason")
                    if isinstance(finish, str):
                        result["finish_reason"] = finish
                    if not result["pass"] and finish == "length":
                        result["reason"] = "output_limit_exhausted"
    except urllib.error.HTTPError as e:
        # Do not echo response bodies, authorization, URLs or prompts.
        result.update(http_status=e.code, reason="http_error")
    except (TimeoutError, OSError, ValueError, TypeError, KeyError, AttributeError) as e:
        result.update(reason="transport_or_protocol_error", error_class=type(e).__name__)
    result["elapsed_ms"] = round((time.monotonic() - started) * 1000)
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--base-url", required=True, help="Explicit OpenAI-compatible API root ending in /v1")
    p.add_argument("--model", required=True)
    p.add_argument("--cases", nargs="+", choices=MODEL_CASES, default=list(MODEL_CASES))
    p.add_argument("--timeout", type=float, default=45)
    p.add_argument("--key-env", help="Read API key from named environment variable; never print it")
    a = p.parse_args()
    if not 1 <= a.timeout <= 180 or not a.model.strip():
        p.error("timeout must be 1..180 seconds and model cannot be blank")
    try:
        url = endpoint(a.base_url)
    except ValueError as e:
        p.error(str(e))
    key = os.environ.get(a.key_env) if a.key_env else None
    if a.key_env and not key:
        p.error("requested key environment variable is absent")
    results = []
    for case in a.cases:
        r = run_case(url, a.model, case, a.timeout, key)
        results.append(r)
        # Emit a progress receipt without disclosing a credential or prompt.
        print(json.dumps({"event": "case_complete", **r}), flush=True)
    passed = sum(r["pass"] for r in results)
    print(json.dumps({"event": "summary", "model": a.model, "synthetic": True,
                      "passed": passed, "total": len(results), "results": results}), flush=True)
    raise SystemExit(0 if passed == len(results) else 2)


if __name__ == "__main__":
    main()
