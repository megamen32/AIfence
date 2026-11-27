// Verify the actual stdio launcher with the official MCP client.
// Executes one upstream top_requests call, which may be billed by Yandex.
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const app = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const runtime = path.join(app, ".runtime");
const connection = JSON.parse(await readFile(path.join(runtime, "connection.json"), "utf8"));
const transport = new StdioClientTransport({
  command: path.join(app, "scripts/wordstat-mcp"),
  args: [],
  stderr: "pipe"
});
let stderr = "";
transport.stderr?.on("data", chunk => { stderr += chunk.toString(); });
const client = new Client({ name: "aifence-wordstat-mac-verification", version: "1.0.0" });
const args = { phrase: "касперский для бизнеса", numPhrases: 3 };
try {
  await client.connect(transport);
  const tools = await client.listTools();
  if (!tools.tools.some(tool => tool.name === "top_requests")) throw new Error("top_requests is absent");
  const result = await client.callTool({ name: "top_requests", arguments: args }, undefined, { timeout: 55000 });
  if (result.isError) throw new Error("top_requests returned an upstream error");
  let data = result.structuredContent;
  if (!data) {
    const text = (result.content ?? []).filter(item => item.type === "text").map(item => item.text).join("\n");
    data = JSON.parse(text);
  }
  if (data === null || typeof data !== "object") throw new Error("top_requests has no JSON data");
  const receipt = {
    verified_at: new Date().toISOString(),
    consumer: "official MCP JS SDK 1.32.2 via the project stdio launcher",
    bridge: "supergateway@4.3.0",
    provider: "mcp-yandex-wordstat@2.5.0",
    endpoint: connection.url,
    server: client.getServerVersion(),
    instructions: client.getInstructions() ?? null,
    tools: tools.tools.map(({ name, description, inputSchema }) => ({ name, description, inputSchema })),
    call: { name: "top_requests", arguments: args },
    result,
    parsed_data: data,
    interpretation: {
      region_filter: "all regions; regionIds omitted",
      devices_filter: "all devices; devices omitted",
      source_status: "historical_only",
      source_available_through: "2026-10-07",
      exact_top_regions_window_known: false,
      note: "Raw provider tools do not apply the Airlock app's period guards or region-name enrichment."
    }
  };
  await mkdir(runtime, { recursive: true, mode: 0o700 });
  await writeFile(path.join(runtime, "mac-mcp-receipt.json"), JSON.stringify(receipt, null, 2) + "\n", { mode: 0o600 });
  console.log(JSON.stringify({
    status: "ok",
    verified_at: receipt.verified_at,
    launcher: path.join(app, "scripts/wordstat-mcp"),
    server: receipt.server,
    tools: receipt.tools.map(tool => tool.name),
    phrase: args.phrase,
    region_filter: receipt.interpretation.region_filter,
    result: data,
    receipt: path.join(runtime, "mac-mcp-receipt.json"),
    source_status: "historical_only",
    exact_top_regions_window_known: false,
    stderr_bytes: Buffer.byteLength(stderr)
  }));
} catch (error) {
  console.error("Wordstat stdio verification failed: " + error.message);
  process.exitCode = 1;
} finally {
  await client.close();
}
