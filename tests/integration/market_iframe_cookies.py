"""Exercise the versioned Market Watch cookie rules with the local caddy:2-alpine image.

Run with python3 tests/integration/market_iframe_cookies.py using the AIfence Docker context.
The upstream uses only synthetic Set-Cookie headers and has no access to the application.
"""

import http.client
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[2]
APP_HOST = "market-watch.localhost"
NONCE = "__air_relay_nonce=sample; Path=/; Max-Age=900; HttpOnly; SameSite=Lax"
CLEAR = "__air_relay_nonce=; Path=/; Expires=Thu, 01 Jan 1970 00:00:01 GMT; Max-Age=0; HttpOnly; SameSite=Lax"
SESSION = "__air_session=example; Path=/; Max-Age=900; HttpOnly; SameSite=Lax"
OTHER = "__air_session_extra=demo; Path=/; Max-Age=900; HttpOnly; SameSite=Lax"
FIXTURES = {"/start": [NONCE], "/callback": [CLEAR, SESSION], "/refresh": [SESSION]}


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True, timeout=30).strip()


def caddyfile():
    # A real reverse-proxy response exercises the same deferred header handler as production.
    config = [
        "{", "    admin off", "    auto_https off", "}",
        ":8080 {", "    import market-iframe.caddy", "    reverse_proxy 127.0.0.1:8081", "}",
        ":8081 {",
    ]
    for path, cookies in FIXTURES.items():
        config.append(f"    handle {path} {{")
        for cookie in [*cookies, OTHER]:
            config.append(f'        header +Set-Cookie "{cookie}"')
        config.extend(['        respond "fixture"', "    }"])
    config.extend(["    respond 404", "}"])
    return "\n".join(config) + "\n"


class EmbeddedCookiePolicy(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        image = os.environ.get("AIFENCE_CADDY_IMAGE", "caddy:2-alpine")
        cls.container = docker(
            "create", "--pull", "never", "-p", "127.0.0.1::8080", image,
            "caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile",
        )
        cls.addClassCleanup(docker, "rm", "-fv", cls.container)
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory)
            (target / "Caddyfile").write_text(caddyfile(), encoding="utf-8")
            shutil.copyfile(ROOT / "platform/market-iframe.caddy", target / "market-iframe.caddy")
            docker("cp", f"{directory}/.", f"{cls.container}:/etc/caddy")
        docker("start", cls.container)
        cls.port = int(docker("port", cls.container, "8080/tcp").split(":")[-1])
        for _ in range(80):
            try:
                cls.response_cookies("/start", APP_HOST, {})
                break
            except (OSError, http.client.HTTPException):
                time.sleep(0.1)
        else:
            raise RuntimeError("Caddy fixture failed to start: " + docker("logs", cls.container))

    @classmethod
    def response_cookies(cls, path, host, metadata):
        connection = http.client.HTTPConnection("127.0.0.1", cls.port, timeout=3)
        try:
            connection.request("GET", path, headers={"Host": f"{host}:{cls.port}", **metadata})
            response = connection.getresponse()
            if response.status != 200 or response.read() != b"fixture":
                raise AssertionError(f"unexpected fixture response for {path}: {response.status}")
            return response.headers.get_all("Set-Cookie", [])
        finally:
            connection.close()

    def check_flow(self, host, metadata, embedded):
        for path, reserved in FIXTURES.items():
            with self.subTest(host=host, metadata=metadata, path=path):
                expected = [OTHER]
                for cookie in reserved:
                    if embedded:
                        cookie = cookie.replace("SameSite=Lax", "Secure; SameSite=None; Partitioned")
                    expected.append(cookie)
                self.assertCountEqual(self.response_cookies(path, host, metadata), expected)

    def test_embedded_documents_and_subrequests(self):
        self.check_flow(APP_HOST, {"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "cross-site"}, True)
        for dest in ("script", "empty"):
            self.check_flow(APP_HOST, {
                "Sec-Fetch-Dest": dest, "Sec-Fetch-Storage-Access": "none",
            }, True)

    def test_standalone_documents_and_first_party_subrequests(self):
        self.check_flow(APP_HOST, {"Sec-Fetch-Dest": "document"}, False)
        self.check_flow(APP_HOST, {
            "Sec-Fetch-Dest": "document", "Sec-Fetch-Storage-Access": "none",
        }, False)
        self.check_flow(APP_HOST, {"Sec-Fetch-Dest": "empty", "Sec-Fetch-Site": "same-origin"}, False)
        self.check_flow(APP_HOST, {}, False)

    def test_unrelated_origins(self):
        for host in ("localhost", "other.localhost", "market-watch.localhost.example"):
            self.check_flow(host, {"Sec-Fetch-Dest": "iframe"}, False)


if __name__ == "__main__":
    unittest.main(verbosity=2)
