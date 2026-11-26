const { test } = require("node:test");
const assert = require("node:assert/strict");

global.HTMLElement = class {};
let chart;
global.customElements = {
  get() {},
  define(_name, constructor) { chart = constructor; }
};
require("./static/price-history.js");

test("a twenty-kopeck spread on a 6739-ruble license uses at least half the plotting height", () => {
  const axis = chart.priceScale(6739, 6739.20);
  assert.ok(axis.min < 6739 && axis.max > 6739.20);
  assert.ok(0.20 / (axis.max - axis.min) >= 0.5);
  assert.ok(axis.ticks.every(t => /,\d{2}$/.test(t.label)));
  assert.ok(axis.ticks.some(t => t.value === 6739.20));
});

test("flat, free, one-kopeck, and wide price ranges have a finite, distinct labeled scale", () => {
  for (const [min, max] of [[6739, 6739], [0, 0], [0, 0.01], [99.99, 100], [3.19, 7000000.05]]) {
    const axis = chart.priceScale(min, max);
    assert.ok(axis.min >= 0 && axis.min <= min && axis.max >= max && axis.max > axis.min);
    assert.ok(axis.ticks.length >= 2 && axis.ticks.length <= 8);
    assert.equal(new Set(axis.ticks.map(t => t.label)).size, axis.ticks.length);
    assert.ok(axis.ticks.every((t, i) => Number.isFinite(t.value) && (!i || t.value > axis.ticks[i-1].value)));
  }
});
