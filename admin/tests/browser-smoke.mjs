// Optional browser checks: run against Vite or preview. All API traffic is mocked;
// this script never reads or changes a real learning database.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
const { chromium } = await import(
  process.env.ADMIN_PLAYWRIGHT_MODULE || "playwright"
);
const browser = await chromium.launch({
  headless: true,
  ...(process.env.ADMIN_BROWSER_PATH
    ? { executablePath: process.env.ADMIN_BROWSER_PATH }
    : {}),
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1050 } });
const errors = [];
page.on("pageerror", (error) => errors.push(error.message));
await page.clock.setFixedTime(new Date("2026-09-11T12:00:00Z"));
const owner = "demo";
const fields = [
  "id",
  "owner_key",
  "term",
  "context",
  "learning_status",
  "personal_interest",
  "usefulness",
  "tags_json",
  "created_at",
  "updated_at",
];
const rows = Array.from({ length: 65 }, (_, i) => ({
  id: String(i),
  owner_key: owner,
  term: ["serendipity", "in a nutshell", "resilient", "a silver lining"][i % 4],
  context: `Meaning ${i + 1}`,
  learning_status: ["new", "learning", "learned", "archived"][i % 4],
  personal_interest: ["low", "normal", "high"][i % 3],
  usefulness: "high",
  tags_json: '["conversation"]',
  created_at: "2026-09-11T10:00:00Z",
  updated_at: "2026-09-11T10:00:00Z",
}));
const calls = [];
let failTable = false,
  emptyAnalytics = false,
  emptySuggestions = false;
let holdTable;
await page.route("**/admin/api/**", async (route) => {
  const url = new URL(route.request().url());
  assert.equal(
    route.request().method(),
    "GET",
    "Browser smoke must not mutate data",
  );
  const endpoint = url.pathname.replace("/admin/api", "");
  const q = Object.fromEntries(url.searchParams);
  calls.push({ endpoint, ...q });
  let body;
  if (endpoint === "/session") body = { owner, version: 1 };
  else if (endpoint === "/tables")
    body = ["vocabulary_items", "review_attempts", "learning_cards"].map(
      (name) => ({
        name,
        count: 65,
        sql: "CREATE TABLE demo (id TEXT)",
        columns: (name === "vocabulary_items"
          ? fields
          : [
              "id",
              "vocabulary_item_id",
              "owner_key",
              "rating",
              "comment",
              "reviewed_at",
            ]
        ).map((name) => ({
          name,
          type: "TEXT",
          notNull: false,
          primaryKey: name === "id",
        })),
      }),
    );
  else if (endpoint === "/analytics")
    body = {
      owner,
      statuses: emptyAnalytics ? [] : [{ label: "learning", count: 65 }],
      ratings: emptyAnalytics
        ? []
        : [
            { label: "good", count: 20 },
            { label: "again", count: 4 },
          ],
      effectiveRatings: [],
      activity: emptyAnalytics
        ? []
        : [
            { day: "2026-09-11", reviews: 6, recalled: 4 },
            { day: "2026-09-10", reviews: 3, recalled: 2 },
          ],
      due: [{ count: emptyAnalytics ? 0 : 8 }],
      comments: [{ count: emptyAnalytics ? 0 : 7 }],
      difficult: emptyAnalytics
        ? []
        : Array.from({ length: 7 }, (_, i) => ({
            id: String(i),
            term: `Attention word ${i}`,
            lapses: 7 - i,
            consecutive_failures: 1,
            difficulty: 6,
          })),
    };
  else if (endpoint.startsWith("/vocabulary/")) {
    const row = rows.find((row) => row.id === endpoint.split("/").at(-1));
    assert.ok(row, "The editor must request an existing fixture item");
    body = {
      itemId: row.id,
      revision: 1,
      term: row.term,
      normalizedTerm: row.term,
      status: row.learning_status,
      usefulness: row.usefulness,
      personalInterest: row.personal_interest,
      tags: [],
      notes: [],
      examples: [],
      createdAt: row.created_at,
      updatedAt: row.updated_at,
    };
  } else if (endpoint === "/suggestions")
    body = {
      owner,
      generatedAt: "2026-09-11T12:00:00Z",
      total: emptySuggestions ? 0 : 2,
      selectable: emptySuggestions ? 0 : 1,
      limit: 50,
      offset: 0,
      rows: emptySuggestions
        ? []
        : [
            {
              vocabularyItemId: "1",
              cardId: "1",
              term: "serendipity",
              context: "An unexpected discovery",
              status: "learning",
              usefulness: "high",
              probability: 1,
              pool: "review",
              reason: "due",
              dueAt: "2026-09-11T10:00:00Z",
            },
            {
              vocabularyItemId: "2",
              cardId: "2",
              term: "resilient",
              status: "learned",
              usefulness: "normal",
              probability: 0,
              pool: "review",
              reason: "not_due",
              dueAt: "2026-09-15T10:00:00Z",
            },
          ],
    };
  else if (endpoint.startsWith("/tables/")) {
    if (holdTable) await holdTable;
    if (failTable)
      return route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({
          error: { message: "Fixture table unavailable" },
        }),
      });
    let filtered = rows.filter(
      (row) => !q.column || String(row[q.column]) === q.value,
    );
    if (q.q) filtered = filtered.filter((row) => row.term.includes(q.q));
    body = {
      total: filtered.length,
      offset: Number(q.offset),
      limit: Number(q.limit),
      rows: filtered.slice(
        Number(q.offset),
        Number(q.offset) + Number(q.limit),
      ),
    };
  } else throw new Error(`Unexpected endpoint: ${endpoint}`);
  await route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(body),
  });
});
const nav = () =>
  page.getByRole("navigation", { name: "Main navigation", exact: true });
async function navigate(name) {
  if (!(await nav().isVisible()))
    await page.getByRole("button", { name: "Menu", exact: true }).click();
  await nav().getByRole("button", { name, exact: true }).click();
}
async function settled() {
  await page.waitForFunction(
    () =>
      !document.querySelector('.records-scroll[aria-busy="true"]') &&
      !document.querySelector(".loading"),
  );
}
const lastTable = () =>
  calls.filter((call) => call.endpoint.startsWith("/tables/")).at(-1);
async function screenshot(name) {
  if (!process.env.ADMIN_SCREENSHOT_DIR) return;
  await mkdir(process.env.ADMIN_SCREENSHOT_DIR, { recursive: true });
  await page.screenshot({
    path: `${process.env.ADMIN_SCREENSHOT_DIR}/${name}.png`,
    fullPage: true,
  });
}
try {
  await page.goto(process.env.ADMIN_TEST_URL || "http://127.0.0.1:5173/admin/");
  await page
    .getByLabel("Admin token", { exact: true })
    .fill("browser-fixture-token");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("heading", { name: "Overview", exact: true }).waitFor();
  await page.getByText("reviews today", { exact: true }).waitFor();
  assert.equal(await page.locator(".today-summary strong").textContent(), "6");
  assert.ok(
    await page
      .getByRole("heading", { name: "8 cards are due for review." })
      .isVisible(),
  );
  assert.equal(
    await page
      .locator(".stats > div strong")
      .allTextContents()
      .then((values) => values.join(",")),
    "65,8,24,83%",
  );
  assert.equal(
    await page
      .locator("section")
      .filter({
        has: page.getByRole("heading", {
          name: "Words needing attention",
          exact: true,
        }),
      })
      .locator("tbody tr")
      .count(),
    5,
    "Overview shows the five highest-priority attention words",
  );
  assert.ok(
    await page
      .getByRole("img", {
        name: "2026-09-11: 6 reviews, 4 good/easy",
        exact: true,
      })
      .isVisible(),
  );
  await page
    .getByRole("button", { name: "Attention word 0", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "Vocabulary item", exact: true })
    .waitFor();
  await page.locator(".item-heading h3").waitFor();
  assert.equal(calls.at(-1).endpoint, "/vocabulary/0");
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await navigate("Analytics");
  await settled();
  assert.equal(
    await page
      .locator("section")
      .filter({
        has: page.getByRole("heading", {
          name: "Words needing attention",
          exact: true,
        }),
      })
      .locator("tbody tr")
      .count(),
    7,
    "Analytics retains the full attention list",
  );
  await navigate("Overview");
  await settled();
  await page.getByRole("button", { name: "Browse your vocabulary" }).click();
  await settled();
  assert.equal(lastTable().column, "owner_key");
  assert.equal(lastTable().value, owner);
  await page
    .getByRole("button", { name: "Clear filters", exact: true })
    .click();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await settled();
  assert.equal(lastTable().offset, "50");
  await page.getByRole("button", { name: "Learning", exact: true }).click();
  await settled();
  assert.equal(lastTable().offset, "0");
  assert.equal(lastTable().column, "learning_status");
  assert.equal(lastTable().value, "learning");
  await page.getByRole("button", { name: "Filters & tools" }).click();
  await page
    .getByRole("combobox", { name: "Match field", exact: true })
    .selectOption("term");
  const beforeTyping = calls.length;
  await page
    .getByRole("textbox", { name: "Exact value", exact: true })
    .fill("serendipity");
  assert.equal(
    calls.length,
    beforeTyping,
    "Draft typing must not issue requests",
  );
  await page.getByRole("button", { name: "Apply filter", exact: true }).click();
  await settled();
  assert.equal(lastTable().value, "serendipity");
  await page
    .getByRole("button", { name: "Clear filters", exact: true })
    .click();
  await page.getByRole("button", { name: "Filters & tools" }).click();
  await page.getByRole("button", { name: "All dates", exact: true }).click();
  await page.getByRole("button", { name: "Last 7 days", exact: true }).click();
  await settled();
  assert.equal(lastTable().from, "2026-09-05");
  assert.equal(lastTable().to, "2026-09-11");
  await page
    .getByRole("button", { name: "Remove date filter", exact: true })
    .click();
  await page.getByRole("button", { name: "All dates", exact: true }).click();
  await page.getByRole("textbox", { name: "Start date" }).fill("2026-02-30");
  assert.ok(
    await page.getByRole("button", { name: "Apply", exact: true }).isDisabled(),
  );
  await page.getByRole("textbox", { name: "Start date" }).fill("2026-09-10");
  await page.getByRole("textbox", { name: "End date" }).fill("2026-09-01");
  assert.ok(
    await page.getByRole("button", { name: "Apply", exact: true }).isDisabled(),
  );
  await page.keyboard.press("Escape");
  assert.ok(
    await page
      .getByRole("button", { name: "All dates", exact: true })
      .evaluate((el) => el === document.activeElement),
  );
  assert.equal(lastTable().from, "");
  await page.getByRole("button", { name: "All dates", exact: true }).click();
  await page.locator('[data-day="2026-09-15"]').click();
  await page.locator('[data-day="2026-09-12"]').click();
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await settled();
  assert.equal(lastTable().from, "2026-09-12");
  assert.equal(lastTable().to, "2026-09-15");
  await page
    .getByRole("button", { name: "Remove date filter", exact: true })
    .click();
  await page.getByRole("button", { name: "All dates", exact: true }).click();
  await page.locator('[data-day="2026-09-30"]').focus();
  await page.keyboard.press("ArrowRight");
  assert.ok(
    await page
      .locator('[data-day="2026-10-01"]')
      .evaluate((el) => el === document.activeElement),
  );
  await page.keyboard.press("Escape");
  const rowHeight = await page
    .locator("tbody tr")
    .first()
    .evaluate((row) => row.getBoundingClientRect().height);
  await page.getByRole("button", { name: "Compact rows", exact: true }).click();
  assert.ok(
    (await page
      .locator("tbody tr")
      .first()
      .evaluate((row) => row.getBoundingClientRect().height)) < rowHeight,
  );
  await page.getByRole("button", { name: /^Term/ }).click();
  await settled();
  assert.equal(lastTable().sort, "term");
  assert.equal(lastTable().direction, "asc");
  assert.equal(
    await page.locator("th").first().getAttribute("aria-sort"),
    "ascending",
  );
  await page.getByRole("button", { name: /^Term/ }).click();
  await settled();
  assert.equal(lastTable().direction, "desc");
  assert.equal(
    await page.locator("th").first().getAttribute("aria-sort"),
    "descending",
  );
  const headerTop = await page
    .locator("thead")
    .evaluate((el) => el.getBoundingClientRect().top);
  await page.locator(".records-scroll").evaluate((el) => {
    el.scrollTop = 200;
  });
  assert.equal(
    Math.round(
      await page
        .locator("th")
        .first()
        .evaluate((el) => el.getBoundingClientRect().top),
    ),
    Math.round(headerTop),
  );
  await page.getByRole("button", { name: "Filters & tools" }).click();
  const retainedRows = await page.locator("tbody").textContent();
  let release;
  holdTable = new Promise((resolve) => {
    release = resolve;
  });
  await page.getByRole("button", { name: "Learning", exact: true }).click();
  await page.getByText("Updating records…").waitFor();
  assert.equal(await page.locator("tbody").textContent(), retainedRows);
  assert.ok(
    await page
      .locator("tbody")
      .evaluate((body) =>
        [...body.querySelectorAll("button")].every((button) => button.disabled),
      ),
    "Word, Open, and Raw controls must all be disabled for retained rows",
  );
  assert.ok(
    await page
      .getByRole("button", { name: "Export page", exact: true })
      .isDisabled(),
  );
  release();
  holdTable = undefined;
  await settled();
  assert.equal(lastTable().column, "learning_status");
  assert.equal(await page.locator("tbody tr").count(), 16);
  assert.ok(
    await page
      .getByRole("button", { name: "Export page", exact: true })
      .isEnabled(),
  );
  await page.getByRole("button", { name: "Filters & tools" }).click();
  failTable = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.getByRole("button", { name: "Retry", exact: true }).waitFor();
  assert.equal(await page.locator(".records-scroll").count(), 0);
  failTable = false;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await settled();
  await navigate("Comments");
  await settled();
  await page.getByRole("searchbox").fill("unknownword");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await settled();
  await page
    .getByRole("button", { name: "Clear filters", exact: true })
    .first()
    .click();
  await settled();
  assert.equal(
    lastTable().comments,
    "true",
    "Comments view must stay restricted after clearing filters",
  );
  await navigate("Next suggestions");
  await page.getByText("eligible next", { exact: true }).waitFor();
  assert.equal(
    await page
      .locator(".probability-track span")
      .first()
      .evaluate((el) => el.style.width),
    "100%",
  );
  assert.equal(
    await page
      .locator(".unavailable .probability-track span")
      .evaluate((el) => el.style.width),
    "0%",
  );
  assert.equal(await page.locator(".group-divider").count(), 1);
  await screenshot("suggestions-desktop");
  emptySuggestions = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page
    .getByRole("heading", { name: "No active words to suggest" })
    .waitFor();
  emptyAnalytics = true;
  await navigate("Overview");
  await page
    .getByRole("heading", { name: "Start with a word worth remembering." })
    .waitFor();
  assert.ok(
    await page.getByRole("button", { name: "Add your first word" }).isVisible(),
  );
  emptyAnalytics = false;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await settled();
  await screenshot("overview-desktop");
  for (const width of [390, 760]) {
    await page.setViewportSize({ width, height: 844 });
    assert.ok(
      await page.getByRole("button", { name: "Menu", exact: true }).isVisible(),
    );
    assert.ok(!(await nav().isVisible()));
    await screenshot(`overview-${width}`);
    await navigate("Vocabulary");
    await settled();
    await page.getByRole("button", { name: "All dates", exact: true }).click();
    await screenshot(`calendar-${width}`);
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    );
    await page.keyboard.press("Escape");
    await navigate("Overview");
    await settled();
  }
  assert.deepEqual(errors, []);
  console.log(
    "Browser smoke passed: dashboard, filters, calendar, keyboard, pagination, sort, loading/error recovery, suggestions, empty states, and mobile navigation.",
  );
} finally {
  await browser.close();
}
