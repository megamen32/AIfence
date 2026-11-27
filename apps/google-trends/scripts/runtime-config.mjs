import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
export const APP_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const DEFAULT_ENV_FILE = path.join(APP_ROOT, ".runtime", "hasdata.env");
export const MCP_URL = "https://mcp.hasdata.com/mcp?apis=google_trends";
export class ConfigError extends Error {
  constructor(code, message) { super(message); this.code = code; }
}
export function readKey(envFile = DEFAULT_ENV_FILE) {
  let st;
  try { st = fs.lstatSync(envFile); }
  catch (e) {
    if (e.code === "ENOENT") throw new ConfigError("credentials_required", "HASDATA_API_KEY is not configured.");
    throw new ConfigError("configuration_error", "Cannot read the private credential file.");
  }
  if (!st.isFile() || st.isSymbolicLink() || (process.platform !== "win32" && (st.mode & 0o077))) {
    throw new ConfigError("credential_permissions", "The credential must be a regular owner-only file (0600).");
  }
  const lines = fs.readFileSync(envFile, "utf8").split(/\r?\n/).map(s => s.trim()).filter(s => s && !s.startsWith("#"));
  if (lines.length !== 1 || !lines[0].startsWith("HASDATA_API_KEY=")) {
    throw new ConfigError("configuration_error", "Expected one HASDATA_API_KEY assignment.");
  }
  let value = lines[0].slice("HASDATA_API_KEY=".length).trim();
  if (value.length >= 2 && ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))) value = value.slice(1, -1);
  if (!value.trim() || /[\r\n\x00-\x1f\x7f]/.test(value) || /^(YOUR_|REPLACE_|<)/i.test(value)) {
    throw new ConfigError("credentials_required", "HASDATA_API_KEY is not configured.");
  }
  return value;
}
export function prepareLaunch({ envFile = DEFAULT_ENV_FILE, env = process.env } = {}) {
  const key = readKey(envFile);
  const proxy = path.join(APP_ROOT, "node_modules", "mcp-remote", "dist", "proxy.js");
  if (!fs.existsSync(proxy)) throw new ConfigError("setup_required", "Run the documented npm ci setup first.");
  const childEnv = {
    PATH: env.PATH || "/opt/homebrew/bin:/usr/bin:/bin",
    HOME: env.HOME || "",
    LANG: env.LANG || "en_US.UTF-8",
    HASDATA_API_KEY: key,
    MCP_REMOTE_CONFIG_DIR: path.join(APP_ROOT, ".runtime", "mcp-auth"),
    TMPDIR: path.join(APP_ROOT, ".runtime", "tmp"),
  };
  for (const name of ["SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"]) if (env[name]) childEnv[name] = env[name];
  return {
    command: process.execPath,
    args: [proxy, MCP_URL, "--header", "x-api-key:${HASDATA_API_KEY}", "--transport", "http-only"],
    options: { cwd: APP_ROOT, env: childEnv, stdio: "inherit" },
  };
}
