#!/usr/bin/env python3
"""Submit or inspect the synthetic QA conversation in ordinary Chrome on the local Mac."""
import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path

sys.dont_write_bytecode = True
from acceptance import ROOT, SDK, check, fixture, prepare_chat, register

ORIGIN = "http://localhost:42080"
NEW_TAB = """
on run argv
 tell application "Google Chrome"
  repeat with w in windows
   if (count of (tabs of w whose URL starts with "http://localhost:42080/")) > 0 then
    set t to make new tab at end of tabs of w with properties {URL:item 1 of argv}
    return id of t
   end if
  end repeat
 end tell
 error "Open the local Airlock in ordinary Chrome first."
end run
"""
JAVASCRIPT = """
on run argv
 set wanted to (item 1 of argv) as integer
 tell application "Google Chrome"
  repeat with w in windows
   set found to (tabs of w whose id is wanted)
   if (count found) > 0 then
    set t to item 1 of found
    if not ((URL of t) starts with "http://localhost:42080/") then error "Unexpected tab origin"
    return execute t javascript (item 2 of argv)
   end if
  end repeat
 end tell
 error "QA tab is closed"
end run
"""


def apple(script, *args):
    return subprocess.run(["/usr/bin/osascript", "-", *map(str, args)], input=script,
                          capture_output=True, text=True, timeout=15, check=True).stdout.strip()


def js(tab, code):
    return apple(JAVASCRIPT, tab, code)


def begin(args):
    initial = register(SDK(args), fixture("synthetic-input.json"))
    seed = prepare_chat(SDK(args), initial)
    tab = apple(NEW_TAB, ORIGIN + "/chat?app=product-manager")
    print(json.dumps({"tab": tab, "hypothesis_id": seed["hypothesis_id"],
                      "snapshot_id": initial}), flush=True)
    for _ in range(40):
        if js(tab, "String(!!document.querySelector('.composer-field textarea:not([readonly]):not(:disabled)'))") == "true":
            break
        time.sleep(0.5)
    else:
        raise RuntimeError("The signed-in chat composer did not open")
    check(js(tab, "location.pathname") == "/chat", "Refusing to post into an existing conversation")
    code = """(() => {
      const input = document.querySelector('.composer-field textarea:not([readonly])');
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, PROMPT);
      input.dispatchEvent(new Event('input', {bubbles: true}));
      return String(input.value === PROMPT);
    })()""".replace("PROMPT", json.dumps(seed["prompt"], ensure_ascii=False))
    check(js(tab, code) == "true", "The composer rejected the synthetic request")
    sent = js(tab, """(() => {
      const send = document.querySelector('button.composer-btn:not(.composer-stop):not(:disabled)');
      if (!send) return 'unavailable';
      send.click(); return 'clicked';
    })()""")
    check(sent == "clicked", "The normal send button is unavailable")
    return {"tab": tab, "hypothesis_id": seed["hypothesis_id"], "submitted": True}


def observe(tab):
    code = """JSON.stringify((() => {
      const messages = document.querySelector('.chat-messages');
      const responses = [...(messages?.querySelectorAll('[data-transcript-key] .msg-response') || [])];
      const last = responses.at(-1);
      const pending = messages?.querySelector('.permission-confirmation,.permission-card');
      return {path:location.pathname, active:!!document.querySelector('button.composer-stop'),
        confirmation:!!pending, message_count:responses.length, reply:last?.innerText.slice(-6500) || '',
        error:messages?.querySelector('.chat-load-error')?.innerText || ''};
    })())"""
    report = json.loads(js(tab, code))
    report["url"] = ORIGIN + report["path"]
    return report


def main():
    p = argparse.ArgumentParser(description=__doc__)
    mode = p.add_mutually_exclusive_group(required=True)
    mode.add_argument("--begin", action="store_true", help="Submit one synthetic prompt in a fresh tab.")
    mode.add_argument("--observe", metavar="TAB_ID", help="Read this test tab's visible chat only.")
    p.add_argument("--air", default=os.environ.get("AIR", str(ROOT / ".runtime/bin/market-air")))
    args = p.parse_args()
    args.origin = ORIGIN
    result = begin(args) if args.begin else observe(args.observe)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
