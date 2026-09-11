import { strict as assert } from "node:assert";
import { test } from "node:test";
import { chance, chanceWidth } from "./suggestionDisplay.ts";

test("probability display distinguishes tiny positive chances from ineligible cards", () => {
  assert.equal(chance(0), "0%");
  assert.equal(chance(0.000001), "<0.01%");
  assert.equal(chance(0.0001), "0.01%");
  assert.equal(chance(0.125), "12.5%");
  assert.equal(chance(1), "100%");
});
test("relative bars share the page scale and safely handle an all-ineligible page", () => {
  assert.equal(chanceWidth(0.125, 0.25), 50);
  assert.equal(chanceWidth(0.25, 0.25), 100);
  assert.equal(chanceWidth(0, 0.25), 0);
  assert.equal(chanceWidth(0, 0), 0);
  assert.equal(chanceWidth(0.3, 0.25), 100);
});
