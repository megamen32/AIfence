#!/usr/bin/env python3
"""Check the public Softline KES path and emit one PM evidence batch."""

import argparse
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path
from tempfile import mkdtemp
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4

from playwright.sync_api import Error, TimeoutError, sync_playwright

STORE = "https://store.softline.ru"
KES_URL = STORE + "/kaspersky/kaspersky-endpoint-security-dlya-biznesa-standartnyiy/"
CART_URL = STORE + "/basket/"
KES = re.compile(r"Kaspersky Endpoint Security", re.I)
PRODUCT_KEY = "kaspersky_endpoint_security"
WATCH = re.compile(r"/watch/\d+(?:/\d+)?/?")
METRIKA = {"mc.yandex.ru", "mc.yandex.com"}
EVENT_WINDOW_MS = 3000


def is_metrika_add(query, form):
    for encoded in (query, form):
        for content in parse_qs(encoded).get("site-info", []):
            try:
                payload = json.loads(content)
            except ValueError:
                continue
            if not isinstance(payload, dict) or not isinstance(payload.get("__ym"), dict):
                continue
            actions = payload["__ym"].get("ecommerce")
            if isinstance(actions, list) and any(
                isinstance(action, dict)
                and isinstance(action.get("add"), dict)
                and action["add"].get("products")
                for action in actions
            ):
                return True
    return False


def check_device(browser, settings, screenshot):
    stage, path, reason, source_url = "entry", "unknown", None, KES_URL
    add_status = None
    watching = False
    metrika_control = False
    metrika_add_sent = False
    window_complete = False

    def on_request(request):
        nonlocal metrika_add_sent
        url = urlsplit(request.url)
        if watching and url.hostname in METRIKA and WATCH.fullmatch(url.path):
            metrika_add_sent |= is_metrika_add(url.query, request.post_data or "")

    def on_response(response):
        nonlocal metrika_control, add_status
        url = urlsplit(response.url)
        if url.hostname in METRIKA and WATCH.fullmatch(url.path) and 200 <= response.status < 300:
            metrika_control = True
        if (
            watching
            and url.hostname == "store.softline.ru"
            and url.path == "/basket/add/"
            and response.request.method == "POST"
        ):
            add_status = response.status

    with browser.new_context(**settings, locale="ru-RU") as context:
        page = context.new_page()
        page.set_default_timeout(5000)
        page.on("request", on_request)
        page.on("response", on_response)
        try:
            entry = page.goto(KES_URL, wait_until="domcontentloaded", timeout=12000)
            if not entry or not entry.ok or urlsplit(page.url).hostname != "store.softline.ru":
                reason = "entry_unavailable"
            else:
                page.get_by_role("heading", level=1, name=KES).wait_for()
                stage = "configuration"
                cookie = page.locator("button.js-accept-use_cookie")
                if cookie.is_visible():
                    cookie.click()
                renewal = page.get_by_text("Продление", exact=True).filter(visible=True).first
                renewal.click()
                if not renewal.evaluate("(label) => label.control?.checked === true"):
                    reason = "configuration_unconfirmed"
                else:
                    stage = "add_to_cart"
                    add = page.get_by_role("button", name="Добавить в корзину", exact=True)
                    watching = True
                    with page.expect_response(
                        lambda r: urlsplit(r.url).hostname == "store.softline.ru"
                        and urlsplit(r.url).path == "/basket/add/"
                        and r.request.method == "POST",
                    ):
                        add.filter(visible=True).first.click()

                    page.wait_for_timeout(EVENT_WINDOW_MS)
                    window_complete = True
                    watching = False
                    if add_status is not None and add_status >= 500:
                        path, reason = "broken", "cart_add_http_5xx"
                    elif add_status is None or not 200 <= add_status < 300:
                        reason = "cart_add_unconfirmed"
                    else:
                        stage = "open_cart"
                        page.get_by_role("link", name="Перейти в корзину", exact=True).click()
                        page.wait_for_url(CART_URL, wait_until="domcontentloaded")
                        source_url = CART_URL
                        stage = "cart_item"
                        cart_item = page.locator(".js-item-name").filter(has_text=KES)
                        cart_item.filter(has_text=re.compile(r"продление", re.I)).wait_for(timeout=10000)
                        path = "reached"
        except TimeoutError:
            reason = "timeout"
        except Error:
            reason = "playwright_error"

        artifact = None
        if urlsplit(page.url).hostname == "store.softline.ru":
            try:
                page.screenshot(path=screenshot, timeout=3000)
                screenshot.chmod(0o600)
                artifact = screenshot.name
            except Error:
                pass

    if metrika_add_sent:
        metrika = "sent"
    elif window_complete and metrika_control and path == "reached":
        metrika = "not_observed"
    else:
        metrika = "unknown"
    if path == "broken":
        outcome = "path_broken"
    elif path == "reached":
        outcome = {"sent": "ok", "not_observed": "metrika_add_not_observed"}.get(metrika)
    else:
        outcome = None
    return source_url, outcome, {
        "stage": stage,
        "path_status": path,
        "metrika_add": metrika,
        "metrika_watch_http_ok": metrika_control,
        "cart_add_http_status": add_status,
        "event_window_ms": EVENT_WINDOW_MS if window_complete else None,
        "reason": reason,
        "screenshot": artifact,
    }


def check_purchase_path(channel=None, *, artifact_dir=None, chromium_path=None):
    """Return (evidence batch, private screenshot directory)."""
    if artifact_dir is None:
        artifacts = Path(mkdtemp(prefix="aifence-path-check-", dir="/tmp"))
    else:
        artifacts = Path(artifact_dir)
        artifacts.mkdir(mode=0o700, exist_ok=True)
    instant = datetime.now(timezone.utc)
    date = instant.date().isoformat()
    run_id = uuid4().hex
    observations, warnings = [], []
    with sync_playwright() as playwright:
        devices = {
            "desktop": {"viewport": {"width": 1366, "height": 768}},
            "mobile": playwright.devices["Pixel 7"],
        }
        try:
            browser = playwright.chromium.launch(
                channel=channel, executable_path=chromium_path, timeout=12000
            )
        except Error:
            browser = None

        try:
            for device, settings in devices.items():
                if browser:
                    url, outcome, details = check_device(browser, settings, artifacts / f"{device}.png")
                else:
                    url, outcome, details = KES_URL, None, {
                        "stage": "browser",
                        "path_status": "unknown",
                        "metrika_add": "unknown",
                        "reason": "browser_launch_failed",
                    }
                if not outcome or details["metrika_add"] == "unknown":
                    warnings.append(f"{device}: {details['reason'] or 'metrika_add_unknown'}")
                observations.append({
                    "id": f"path-check-{run_id}-{device}",
                    "kind": "path_check",
                    "product_key": PRODUCT_KEY,
                    "metric_key": "purchase_path",
                    "value": outcome,
                    "unit": "verdict",
                    "period": {"from": date, "to": date},
                    "segment": device,
                    "sample_size": 1 if details.get("stage") != "browser" else 0,
                    "source_ref": {
                        "source_id": "softline-public-kes",
                        "locator": f"playwright/{device}/{details['stage']}",
                        "url": url,
                    },
                    "status": "observed" if outcome else "missing",
                    "details": details,
                })
        finally:
            if browser:
                browser.close()

    known = sum(item["status"] == "observed" for item in observations)
    return {
        "schema_version": "aifence.v1",
        "source": "browser",
        "as_of": instant.isoformat(timespec="seconds"),
        "status": "unavailable" if not known else "partial" if warnings else "ok",
        "observations": observations,
        "warnings": warnings,
    }, artifacts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    browser = parser.add_mutually_exclusive_group()
    browser.add_argument("--channel", choices=["chrome"], help="Use installed Google Chrome")
    browser.add_argument("--chromium-path", type=Path, help="Use this installed Chromium executable")
    parser.add_argument("--artifact-dir", type=Path, help="Private scratch directory for screenshots")
    args = parser.parse_args()
    batch, artifacts = check_purchase_path(
        channel=args.channel, artifact_dir=args.artifact_dir, chromium_path=args.chromium_path
    )
    print(artifacts, file=sys.stderr)
    print(json.dumps(batch, ensure_ascii=False))


if __name__ == "__main__":
    main()
