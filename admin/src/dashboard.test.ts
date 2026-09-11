import { strict as assert } from "node:assert";
import { test } from "node:test";
import { activityDays, wordsNeedingAttention } from "./dashboard.ts";

test("activity fills exactly 30 UTC days across year boundaries", () => {
  const days = activityDays(
    [{ day: "2026-01-01", reviews: 7, recalled: 5 }],
    new Date("2026-01-01T23:30:00-08:00"),
  );
  assert.equal(days.length, 30);
  assert.equal(days[0].day, "2025-12-04");
  assert.deepEqual(days.at(-1), { day: "2026-01-02", reviews: 0, recalled: 0 });
  assert.equal(days.at(-2)?.reviews, 7);
});

test("attention excludes healthy cards even if they have high difficulty", () => {
  assert.deepEqual(
    wordsNeedingAttention([
      { id: "healthy", lapses: 0, consecutive_failures: 0, difficulty: 9 },
      { id: "lapsed", lapses: 2, consecutive_failures: 0 },
      { id: "failing", lapses: 0, consecutive_failures: 1 },
    ]).map((row) => row.id),
    ["lapsed", "failing"],
  );
  assert.deepEqual(wordsNeedingAttention([]), []);
});
