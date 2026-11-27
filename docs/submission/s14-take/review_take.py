#!/opt/homebrew/bin/python3.11
"""Record a read-only review of a saved S14 case in the real Mac Product Manager.

Run under the Mac's logged-in GUI session:
  /opt/homebrew/bin/python3.11 review_take.py --out /private/path/new-take
The film/demo account is read from the existing private local login file. The
recording and receipt contain neither login material nor authorization headers.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import time
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit, parse_qsl, urlencode
from urllib.request import Request, urlopen
from playwright.sync_api import sync_playwright

PROJECT = Path("/Users/user/AIfence")
FILM = PROJECT / ".runtime/film-owner-20261127"
ORIGIN = "http://product-manager.localhost:42080"
HYPOTHESIS_ID = "watch-3df8a337b60f0d84c3d02e5b381ae1ca"
TASK_ID = "PM-9fa181b8d949a3da"
PROFILE = PROJECT / ".runtime/film-s14-owner-private/access.json"
CHROME_CACHE = Path.home() / "Library/Caches/ms-playwright"
WIDTH, HEIGHT = 1366, 850


def utc():
    return datetime.now(timezone.utc).isoformat()


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def login_native(request):
    profile = json.loads(PROFILE.read_text())
    res = request.post("http://localhost:42080/auth/login",
        data={k: profile[k] for k in ("email", "password")}, timeout=10000)
    assert res.ok, f"Film user login returned {res.status}"
    session = res.json()
    who = request.get("http://localhost:42080/api/v1/me",
        headers={"Authorization": "Bearer " + session["accessToken"]}, timeout=8000)
    assert who.ok, f"Film session readback returned {who.status}"
    return request.storage_state()


def safe_url(url):
    parts = urlsplit(url)
    keys = {"id", "tab", "mode", "alert", "monitor", "snapshot", "obs"}
    safe_query = urlencode([(k, v) for k, v in parse_qsl(parts.query) if k in keys])
    return urlunsplit((parts.scheme, parts.netloc, parts.path, safe_query, ""))


def media_tool():
    supplied = os.environ.get("AIFENCE_FFMPEG")
    candidates = [Path(supplied)] if supplied else []
    cache = Path.home() / ".cache/uv/archive-v0"
    candidates += sorted(cache.glob("*/lib/python*/site-packages/imageio_ffmpeg/binaries/ffmpeg-macos-*"))
    candidates += [Path("/opt/homebrew/bin/ffmpeg")]
    for candidate in candidates:
        if candidate.is_file() and subprocess.run([str(candidate), "-version"], capture_output=True).returncode == 0:
            return str(candidate)
    raise RuntimeError("No working FFmpeg executable")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=True)
    if any((out / name).exists() for name in ("take.webm", "take.mp4", "receipt.json", "capture.partial.webm")):
        raise SystemExit("Use a new output directory for each take")
    ffmpeg = media_tool()
    chrome = next(CHROME_CACHE.glob("chromium-*/chrome-mac-arm64/*/Contents/MacOS/Google Chrome for Testing"))
    events, documents, observations = [], [], []
    t0 = None
    clock = time.monotonic
    raw = out / "take.webm"
    trace = out / "events.jsonl"

    def log(kind, **data):
        item = {"t": round(clock() - t0, 3) if t0 is not None else None, "action": kind, **data}
        if t0 is not None:
            events.append(item)
            with trace.open("a") as stream:
                stream.write(json.dumps(item, ensure_ascii=False) + "\n")

    def scroll(loc):
        for attempt in range(3):
            try:
                loc.scroll_into_view_if_needed(timeout=3500)
                return
            except Exception as exc:
                if attempt == 2 or "not attached" not in str(exc):
                    raise
                time.sleep(.2)

    with sync_playwright() as driver:
        browser = driver.chromium.launch(executable_path=str(chrome), headless=False,
            args=["--use-mock-keychain", "--password-store=basic", "--disable-background-networking",
                  "--window-size=1420,940", "--window-position=100,60"])
        bootstrap = driver.request.new_context()
        state = login_native(bootstrap)
        bootstrap.dispose()
        assert "airlock_session" in [c["name"] for c in state["cookies"]]
        # Complete the native per-app relay before the recorder starts. The
        # same user retains both the Airlock and Product Manager cookies.
        preflight = browser.new_context(storage_state=state, locale="ru-RU")
        landing = preflight.new_page()
        landing.goto(ORIGIN + "/?mode=demo&tab=overview", wait_until="domcontentloaded")
        landing.get_by_role("link", name="Гипотеза", exact=True).first.wait_for()
        landing.wait_for_timeout(1250)
        landing.locator('a[aria-label^="Kaspersky"]').first.wait_for()
        state = preflight.storage_state()
        preflight.close()
        context = browser.new_context(viewport={"width": WIDTH, "height": HEIGHT}, device_scale_factor=1,
            locale="ru-RU", color_scheme="light", service_workers="block", storage_state=state,
            record_video_dir=str(out / "raw-tmp"), record_video_size={"width": WIDTH, "height": HEIGHT})
        t0 = clock()
        utc_start = utc()
        page = context.new_page()
        video = page.video
        log("capture_start", mode="review", browser_record_video=True)
        page.set_default_timeout(5500)
        page.set_default_navigation_timeout(8500)

        def response(resp):
            if resp.request.resource_type == "document":
                item = {"status": resp.status, "method": resp.request.method, "url": safe_url(resp.url)}
                documents.append(item)
                log("native_document", **item)

        page.on("response", response)
        home = ORIGIN + "/?mode=demo&tab=overview"
        page.goto(home, wait_until="domcontentloaded")
        tile = page.locator('a[aria-label^="Kaspersky"]').first
        tile.wait_for(state="visible")
        page.wait_for_load_state("load")
        page.wait_for_timeout(1250)
        tile.wait_for(state="visible")
        page.mouse.move(65, 78)
        browser_version = browser.version

        def until(second):
            remaining = second - (clock() - t0)
            if remaining > 0:
                page.wait_for_timeout(remaining * 1000)
            if clock() - t0 > 150:
                raise RuntimeError("Review exceeded its scheduling window")

        def move_to(loc):
            scroll(loc)
            box = loc.bounding_box()
            if box:
                page.mouse.move(box["x"] + box["width"] * .48, box["y"] + box["height"] * .52, steps=16)

        def click(loc, label):
            move_to(loc)
            page.wait_for_timeout(170)
            loc.click()
            log("native_click", target=label, url=page.url)

        def text_of(loc):
            return " ".join(loc.inner_text().split())

        def nav(name):
            return page.get_by_role("link", name=name, exact=True).first

        log("catalog", vendors=page.locator(".catalog-tile").count(), selected=tile.get_attribute("aria-label"))
        until(3)
        move_to(tile)
        until(6)
        click(tile, "Kaspersky treemap tile")
        page.get_by_text("Kaspersky Small Office Security", exact=True).first.wait_for()
        until(11)
        trend = page.locator("figure.trend")
        scroll(trend)
        log("signal", visible=text_of(trend)[:170])
        until(15)
        click(nav("Обзор"), "Обзор")
        saved = page.locator("summary").filter(has_text="Сохранённые гипотезы").first
        click(saved, "Сохранённые гипотезы")
        card = page.locator(f'a[href*="id={HYPOTHESIS_ID}"]').filter(has_text="Kaspersky Small Office Security").first
        move_to(card)
        log("saved_record", title=text_of(card))
        until(20)
        if not card.is_visible():
            click(saved, "Сохранённые гипотезы")
        click(card, "Сохранённая гипотеза Kaspersky")
        page.get_by_text("Проверить переход из корзины к оформлению", exact=True).first.wait_for()
        card_link = page.url
        until(25)
        click(nav("Путь покупки"), "Путь покупки")
        path_message = text_of(page.locator("main"))
        assert "Путь покупки" in path_message
        log("purchase_path", visible=path_message[-235:])
        until(29.5)
        click(nav("Гипотеза"), "Гипотеза")
        for n, device, second in [(0, "desktop", 36), (1, "mobile", 46)]:
            sources = page.locator("summary").filter(has_text="Источники").first
            if sources.locator("..").get_attribute("open") is None:
                click(sources, "Источники")
            rows = page.locator("a.monitor-evidence").filter(has_text="checkouts")
            obs = rows.nth(n)
            move_to(obs)
            until(second)
            if not obs.is_visible():
                click(sources, "Источники")
            click(obs, f"Наблюдение checkouts {device}")
            val = page.get_by_text(device, exact=True).first
            if val.count():
                scroll(val)
            body = text_of(page.locator("main"))
            assert device in body and "checkouts" in body
            observations.append({"device": device, "url": page.url})
            log("source_checked", device=device, metric="checkouts")
            until(second + 4.3)
            page.go_back(wait_until="domcontentloaded")
            page.get_by_text("Проверить переход из корзины к оформлению", exact=True).first.wait_for()
        task = page.locator("#hypothesis-actions")
        scroll(task)
        task_text = text_of(task)
        assert TASK_ID in task_text and "Задача в бэклоге" in task_text
        log("existing_task", task_id=TASK_ID, status="В бэклоге", text=task_text)
        until(61)
        click(nav("После релиза"), "После релиза")
        title = page.get_by_role("heading", name="Оформления на 100 добавлений в корзину")
        title.wait_for()
        chart = title.locator("..")
        scroll(chart)
        points = page.locator("svg .release-point")
        assert points.count() == 12, points.count()
        values = [point.get_attribute("aria-label") for point in points.all()]
        log("after_release", point_count=len(values), values=values)
        until(66)
        for n, hold in [(0, 1.8), (7, 2.2), (8, 2.3), (11, 3.5)]:
            point = points.nth(n)
            target = point.locator("circle").first
            if not target.count():
                target = point
            move_to(target)
            log("chart_hover", point=n + 1, value=values[n])
            page.wait_for_timeout(hold * 1000)
        page.mouse.move(80, 86, steps=12)
        until(79)
        log("capture_stop", final_url=page.url)
        elapsed = clock() - t0
        utc_end = utc()
        if len(context.pages) != 1:
            raise RuntimeError("Unexpected additional tab")
        page.close()
        context.close()
        Path(video.path()).rename(raw)
        browser.close()

    mp4 = out / "take.mp4"
    tmp = out / "take.encoding.mp4"
    subprocess.run([ffmpeg, "-nostdin", "-v", "error", "-y", "-i", str(raw),
        "-map", "0:v:0", "-an", "-c:v", "libx264", "-preset", "veryfast",
        "-crf", "26", "-pix_fmt", "yuv420p", "-movflags", "+faststart", str(tmp)], check=True)
    tmp.rename(mp4)
    import re
    def duration(path):
        proc = subprocess.run([ffmpeg, "-hide_banner", "-i", str(path)], capture_output=True, text=True)
        h, m, s = re.search(r"Duration: (\d+):(\d+):([\d.]+)", proc.stderr).groups()
        return round(int(h) * 3600 + int(m) * 60 + float(s), 3)
    durations = {"webm": duration(raw), "mp4": duration(mp4)}
    assert all(55 <= value <= 180 for value in durations.values()), durations
    assert abs(durations["webm"] - durations["mp4"]) < 0.25, durations
    decoded = subprocess.run([ffmpeg,"-v","error","-i",str(raw),"-map","0:v:0","-an","-f","framehash","-"],capture_output=True,text=True,check=True).stdout
    frame_lines = [l for l in decoded.splitlines() if l and not l.startswith("#")]
    native_header = subprocess.run([ffmpeg,"-hide_banner","-i",str(raw)],capture_output=True,text=True).stderr
    assert f"{WIDTH}x{HEIGHT}" in native_header
    assert frame_lines
    frames = {"count":len(frame_lines),"first":frame_lines[0].split(",")[2].strip(),
              "last":frame_lines[-1].split(",")[2].strip(),"time_base":"native framehash output"}
    (out/"raw-framehash.txt").write_text(decoded)

    receipt = {
        "status":"complete", "mode":"review", "case_id":"S14", "conversation_id":None, "hypothesis_id":HYPOTHESIS_ID,
        "card_url":card_link, "task_id":TASK_ID, "source":"native Mac M1 Product Manager",
        "transport":"direct native app navigation in the using ordinary Airlock cookies from an isolated API login; no routes or content interception",
        "owner":"existing isolated S14 film account", "device_observations":observations,
        "after_release_points":values, "task_text":task_text, "browser":browser_version,
        "headed":True, "viewport":[WIDTH, HEIGHT], "pages":1, "capture_sessions":1,
        "preflight":"The isolated app SSO relay completed in a non-recorded bootstrap context before the one recorded page opened.",
        "utc_start":utc_start, "utc_end":utc_end, "wall_seconds":round(elapsed, 3),
        "audio_tracks":0, "stream_duration_seconds":durations, "frames_observed":frames,
        "events":events, "documents":documents, "outputs":{n:{"size":(out/n).stat().st_size,"sha256":sha(out/n)} for n in ["take.webm","take.mp4","events.jsonl","raw-framehash.txt"]},
        "files":{n:{"bytes":(out/n).stat().st_size,"sha256":sha(out/n)} for n in ["take.webm","take.mp4","events.jsonl","raw-framehash.txt"]},
        "source_sha256":sha(Path(__file__))
    }
    (out/"receipt.json").write_text(json.dumps(receipt, ensure_ascii=False, indent=2))
    print(json.dumps({k:receipt[k] for k in ["mode","card_url","stream_duration_seconds","outputs"]},ensure_ascii=False))


if __name__ == "__main__":
    main()
