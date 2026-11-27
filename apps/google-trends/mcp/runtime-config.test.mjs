import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { execFileSync, spawnSync } from "node:child_process";
import { APP_ROOT, MCP_URL, prepareLaunch } from "../scripts/runtime-config.mjs";
const tmpRoot = path.join(APP_ROOT, ".runtime", "test-tmp");
fs.mkdirSync(tmpRoot, { recursive: true, mode: 0o700 });
test("missing credentials fail before the bridge is spawned", () => {
  const p = path.join(tmpRoot, "missing-credential-file");
  const result = spawnSync(process.execPath, [path.join(APP_ROOT, "scripts", "raw-mcp.mjs"), "--check-config", "--credential-file", p], { encoding: "utf8", env: { PATH: process.env.PATH || "" } });
  assert.equal(result.status, 78);
  assert.equal(result.stdout, "");
  assert.deepEqual(JSON.parse(result.stderr), { status: "credentials_required", provider_calls: 0 });
});
test("only the child environment receives the key; argv stays literal", () => {
  const dir = fs.mkdtempSync(path.join(tmpRoot, "config-"));
  const p = path.join(dir, "fixture.env");
  const marker = "OFFLINE_FIXTURE_NOT_A_PROVIDER_KEY";
  fs.writeFileSync(p, "HASDATA_API_KEY=" + marker + "\n", { mode: 0o600 });
  try {
    const config = prepareLaunch({ envFile: p, env: { PATH: "/usr/bin:/bin", HOME: "/Users/user", DEBUG: "*" } });
    assert.equal(config.options.env.HASDATA_API_KEY, marker);
    assert.ok(!JSON.stringify(config.args).includes(marker));
    assert.ok(config.args.includes("x-api-key:${HASDATA_API_KEY}"));
    assert.equal(config.args[1], MCP_URL);
    assert.ok(config.args.includes("http-only"));
    assert.ok(!("DEBUG" in config.options.env));
    const output = execFileSync(process.execPath, [path.join(APP_ROOT, "scripts", "raw-mcp.mjs"), "--check-config", "--credential-file", p], { encoding: "utf8" });
    assert.equal(JSON.parse(output).provider_calls, 0);
    assert.ok(!output.includes(marker));
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});
test("an unsafe credential file is rejected without exposing its contents", () => {
  const dir = fs.mkdtempSync(path.join(tmpRoot, "mode-"));
  const p = path.join(dir, "fixture.env");
  fs.writeFileSync(p, "HASDATA_API_KEY=OFFLINE_FIXTURE_NOT_A_PROVIDER_KEY\n", { mode: 0o644 });
  fs.chmodSync(p, 0o644);
  try { assert.throws(() => prepareLaunch({ envFile: p }), e => e.code === "credential_permissions"); }
  finally { fs.rmSync(dir, { recursive: true, force: true }); }
});

test("quoted whitespace is missing credentials before bridge spawn", () => {
  const dir = fs.mkdtempSync(path.join(tmpRoot, "blank-"));
  const p = path.join(dir, "fixture.env");
  fs.writeFileSync(p, 'HASDATA_API_KEY="   "\n', { mode: 0o600 });
  try {
    const result = spawnSync(process.execPath, [path.join(APP_ROOT, "scripts", "raw-mcp.mjs"), "--check-config", "--credential-file", p], { encoding: "utf8" });
    assert.equal(result.status, 78);
    assert.deepEqual(JSON.parse(result.stderr), { status: "credentials_required", provider_calls: 0 });
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});
