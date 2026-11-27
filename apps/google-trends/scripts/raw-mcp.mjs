#!/usr/bin/env node
import fs from "node:fs";
import { spawn } from "node:child_process";
import { prepareLaunch, ConfigError, DEFAULT_ENV_FILE } from "./runtime-config.mjs";
const args = process.argv.slice(2);
const check = args.includes("--check-config");
const envIndex = args.indexOf("--credential-file");
const allowed = new Set(["--check-config", "--credential-file"]);
if (envIndex >= 0 && args[envIndex + 1]) allowed.add(args[envIndex + 1]);
if (args.some(a => !allowed.has(a)) || (envIndex >= 0 && !args[envIndex + 1])) {
  process.stderr.write(JSON.stringify({ status: "invalid_arguments" }) + "\n"); process.exit(64);
}
try {
  const config = prepareLaunch({ envFile: envIndex >= 0 ? args[envIndex + 1] : DEFAULT_ENV_FILE });
  if (check) {
    process.stdout.write(JSON.stringify({ status: "configured", provider_calls: 0, credential_value_exposed: false }) + "\n");
  } else {
    for (const dir of [config.options.env.MCP_REMOTE_CONFIG_DIR, config.options.env.TMPDIR]) fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    const child = spawn(config.command, config.args, config.options);
    child.once("error", () => { process.stderr.write(JSON.stringify({ status: "launcher_error" }) + "\n"); process.exitCode = 1; });
    for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => child.kill(sig));
    child.once("exit", code => { process.exitCode = code ?? 1; });
  }
} catch (e) {
  process.stderr.write(JSON.stringify({ status: e instanceof ConfigError ? e.code : "configuration_error", provider_calls: 0 }) + "\n");
  process.exitCode = 78;
}
