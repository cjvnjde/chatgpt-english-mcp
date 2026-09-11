import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
  calendarDays,
  rangeError,
  recentRange,
  shiftDay,
  shiftMonth,
  validDay,
} from "./dateRange.ts";
import {
  dateFilterColumn,
  filterOptions,
  quickFilterColumn,
} from "./tableFilters.ts";

test("calendar is a Monday-first six-week grid including leap days", () => {
  const days = calendarDays("2024-02-17");
  assert.equal(days.length, 42);
  assert.equal(days[0], "2024-01-29");
  assert.equal(days.at(-1), "2024-03-10");
  assert.ok(days.includes("2024-02-29"));
  assert.equal(calendarDays("2026-06-15")[0], "2026-06-01");
});
test("month and day navigation crosses year boundaries without date overflow", () => {
  assert.equal(shiftMonth("2026-01-31", 1), "2026-02-01");
  assert.equal(shiftMonth("2026-01-01", -1), "2025-12-01");
  assert.equal(shiftDay("2024-03-01", -1), "2024-02-29");
});
test("recent presets use inclusive UTC dates, not local dates", () => {
  const now = new Date("2026-01-01T23:30:00-08:00");
  assert.deepEqual(recentRange(1, now), {
    from: "2026-01-02",
    to: "2026-01-02",
  });
  assert.deepEqual(recentRange(7, now), {
    from: "2025-12-27",
    to: "2026-01-02",
  });
  assert.deepEqual(recentRange(30, now), {
    from: "2025-12-04",
    to: "2026-01-02",
  });
});
test("range validation allows open ends but rejects invalid and reversed dates", () => {
  for (const invalid of [
    "2026-02-29",
    "2026-13-01",
    "2026-01-40",
    "01/02/2026",
    "junk",
  ])
    assert.equal(validDay(invalid), false);
  assert.equal(validDay("2024-02-29"), true);
  assert.ok(rangeError({ from: "2026-01-10", to: "2026-01-01" }));
  assert.ok(rangeError({ from: "2026-02-30", to: "" }));
  for (const range of [
    { from: "", to: "" },
    { from: "2026-01-10", to: "" },
    { from: "", to: "2026-01-10" },
    { from: "2026-01-10", to: "2026-01-10" },
  ])
    assert.equal(rangeError(range), "");
});
test("filter configuration reflects server date precedence and known enum fields", () => {
  assert.equal(
    dateFilterColumn(["created_at", "shown_at", "reviewed_at"]),
    "reviewed_at",
  );
  assert.equal(dateFilterColumn(["updated_at", "created_at"]), "created_at");
  assert.equal(dateFilterColumn(["due_at"]), undefined);
  assert.equal(quickFilterColumn("review_attempts"), "rating");
  assert.deepEqual(filterOptions("learning_status"), [
    "new",
    "learning",
    "learned",
    "archived",
  ]);
  assert.deepEqual(filterOptions("personal_interest"), [
    "low",
    "normal",
    "high",
  ]);
  assert.deepEqual(filterOptions("unknown"), []);
});
