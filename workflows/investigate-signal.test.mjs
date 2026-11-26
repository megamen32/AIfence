import {readFileSync} from "node:fs";
import assert from "node:assert/strict";
import test from "node:test";

const AsyncFunction = Object.getPrototypeOf(async function() {}).constructor;
const run = new AsyncFunction("args", "platform", "apps", readFileSync(new URL("./investigate-signal.js", import.meta.url), "utf8"));
const series = {product_key: "synthetic:atlas", control_product_key: "synthetic:lumen", metric_key: "orders",
  from: "2026-04-06", to: "2026-04-12"};
function setup(value, origin = "synthetic_fixture") {
  const calls = [];
  let document;
  const point = {metric_key: "watchdog.alert", period: {from: series.from, to: series.to},
    status: value === null ? "missing" : "observed", value, details: {origin, reason: "test",
      baseline_period: {from: "2026-02-16", to: "2026-03-15"}}};
  const batch = (source, observations) => ({schema_version: "aifence.v1", source,
    as_of: "2026-04-12T23:59:59Z", status: "ok", observations, warnings: []});
  const apps = {metrika_analyst: {tools: {replay_signals: () => batch("sales", [point]),
    releases_in_window: () => batch("release", []), backlog_for_metrics: () => batch("backlog", []),
    data_breaks_in_window: () => batch("metrika", [])}},
    product_manager: {tools: {
      import_evidence: (input) => { document = JSON.parse(input.document); return {snapshot_id: "snapshot"}; },
      investigate_orders: () => ({hypothesis: {id: "card", product_key: series.product_key}, evidence_snapshot_id: "snapshot"}),
      send_to_backlog: (input) => { calls.push(input); return {id: "task"}; }
    }}};
  return {call: args => run({series, ...args}, {inspectApp: () => {}}, apps),
    calls, document: () => document};
}

test("a quiet week and an unknown week cause no investigation", async () => {
  for (const [value, status] of [[false, "quiet"], [null, "incomplete"]]) {
    const env = setup(value);
    assert.equal((await env.call({})).status, status);
    assert.equal(env.document(), undefined);
  }
});
test("historical signals retain the closed-project policy and require backlog confirmation", async () => {
  const env = setup(true, "imported_historical");
  const result = await env.call({});
  assert.equal(result.hypothesis_id, "card");
  assert.equal(env.document().source_policy, "local_project_only");
  assert.equal(env.calls.length, 0);
  assert.equal((await env.call({confirm_backlog: true})).task_id, "task");
  assert.deepEqual(env.calls, [{confirmed: true, hypothesis_id: "card", idempotency_key: "card/backlog"}]);
});
