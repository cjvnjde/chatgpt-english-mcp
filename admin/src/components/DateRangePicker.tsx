import {
  createMemo,
  createSignal,
  createUniqueId,
  For,
  onCleanup,
  onMount,
  Show,
} from "solid-js";
import {
  calendarDays,
  dayDate,
  rangeError,
  rangeLabel,
  recentRange,
  shiftDay,
  shiftMonth,
  utcDay,
  validDay,
  type DateRange,
} from "../dateRange";

export default function DateRangePicker(props: {
  value: DateRange;
  field: string;
  change: (range: DateRange) => void;
}) {
  const id = createUniqueId();
  let root!: HTMLDivElement;
  let trigger!: HTMLButtonElement;
  let panel!: HTMLDivElement;
  const [open, setOpen] = createSignal(false);
  const [draft, setDraft] = createSignal<DateRange>({ from: "", to: "" });
  const [selecting, setSelecting] = createSignal<"from" | "to">("from");
  const [month, setMonth] = createSignal(utcDay());
  const [focused, setFocused] = createSignal(utcDay());
  const days = createMemo(() => calendarDays(month()));
  const weeks = () =>
    Array.from({ length: 6 }, (_, i) => days().slice(i * 7, i * 7 + 7));
  const close = (restore = true) => {
    setOpen(false);
    if (restore) trigger.focus();
  };
  const apply = (range: DateRange) => {
    if (rangeError(range)) return;
    props.change(range);
    close();
  };
  const toggle = () => {
    if (open()) return close();
    setDraft({ ...props.value });
    setSelecting("from");
    const start = props.value.from || props.value.to || utcDay();
    setMonth(start);
    setFocused(start);
    setOpen(true);
    queueMicrotask(() =>
      panel.querySelector<HTMLInputElement>("input")?.focus(),
    );
  };
  const selectDay = (day: string) => {
    setFocused(day);
    if (selecting() === "from" || !validDay(draft().from)) {
      setDraft({ from: day, to: "" });
      setSelecting("to");
    } else {
      const from = draft().from;
      setDraft(day < from ? { from: day, to: from } : { from, to: day });
      setSelecting("from");
    }
  };
  const moveFocus = (day: string) => {
    setMonth(day);
    setFocused(day);
    queueMicrotask(() =>
      panel.querySelector<HTMLButtonElement>(`[data-day="${day}"]`)?.focus(),
    );
  };
  const calendarKey = (event: KeyboardEvent, day: string) => {
    const weekday = (dayDate(day).getUTCDay() + 6) % 7;
    const movements: Record<string, () => string> = {
      ArrowLeft: () => shiftDay(day, -1),
      ArrowRight: () => shiftDay(day, 1),
      ArrowUp: () => shiftDay(day, -7),
      ArrowDown: () => shiftDay(day, 7),
      Home: () => shiftDay(day, -weekday),
      End: () => shiftDay(day, 6 - weekday),
      PageUp: () => shiftMonth(day, -1),
      PageDown: () => shiftMonth(day, 1),
    };
    if (movements[event.key]) {
      event.preventDefault();
      moveFocus(movements[event.key]());
    }
  };
  onMount(() => {
    const outside = (event: PointerEvent) => {
      if (open() && !root.contains(event.target as Node)) close(false);
    };
    document.addEventListener("pointerdown", outside);
    onCleanup(() => document.removeEventListener("pointerdown", outside));
  });
  return (
    <div
      class="date-range"
      ref={root}
      onFocusOut={(event) => {
        if (event.relatedTarget && !root.contains(event.relatedTarget as Node))
          close(false);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open()) {
          event.preventDefault();
          event.stopPropagation();
          close();
        }
      }}
    >
      <button
        ref={trigger}
        classList={{
          "filter-trigger": true,
          selected: !!(props.value.from || props.value.to),
        }}
        aria-expanded={open()}
        aria-controls={id}
        onClick={toggle}
      >
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          aria-hidden="true"
        >
          <rect x="3" y="5" width="18" height="16" rx="2" />
          <path d="M16 3v4M8 3v4M3 11h18" />
        </svg>
        {rangeLabel(props.value)}
        <span class="muted" aria-hidden="true">
          ▾
        </span>
      </button>
      <Show when={open()}>
        <div
          ref={panel}
          id={id}
          class="date-popover"
          role="region"
          aria-label={`${props.field} date range`}
        >
          <strong>{props.field} date</strong>
          <small>UTC · end date included</small>
          <div class="date-presets" aria-label="Date presets">
            <For
              each={[
                { label: "Today", days: 1 },
                { label: "Last 7 days", days: 7 },
                { label: "Last 30 days", days: 30 },
              ]}
            >
              {(preset) => (
                <button onClick={() => apply(recentRange(preset.days))}>
                  {preset.label}
                </button>
              )}
            </For>
          </div>
          <div class="range-inputs">
            <For each={["from", "to"] as const}>
              {(key) => (
                <label classList={{ choosing: selecting() === key }}>
                  {key === "from" ? "Start date" : "End date"}
                  <input
                    aria-describedby={`${id}-help`}
                    aria-invalid={!!rangeError(draft())}
                    placeholder="YYYY-MM-DD"
                    value={draft()[key]}
                    onFocus={() => setSelecting(key)}
                    onInput={(e) => {
                      const value = e.currentTarget.value;
                      setDraft((current) => ({ ...current, [key]: value }));
                      if (validDay(value)) {
                        setMonth(value);
                        setFocused(value);
                      }
                    }}
                  />
                </label>
              )}
            </For>
          </div>
          <div class="calendar-heading">
            <button
              aria-label="Previous month"
              onClick={() => {
                const next = shiftMonth(month(), -1);
                setMonth(next);
                setFocused(next);
              }}
            >
              ‹
            </button>
            <strong aria-live="polite">
              {dayDate(month()).toLocaleDateString(undefined, {
                month: "long",
                year: "numeric",
                timeZone: "UTC",
              })}
            </strong>
            <button
              aria-label="Next month"
              onClick={() => {
                const next = shiftMonth(month(), 1);
                setMonth(next);
                setFocused(next);
              }}
            >
              ›
            </button>
          </div>
          <div class="calendar" role="grid" aria-label="Choose dates">
            <div class="calendar-week" role="row">
              <For each={["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"]}>
                {(day) => <span role="columnheader">{day}</span>}
              </For>
            </div>
            <For each={weeks()}>
              {(week) => (
                <div class="calendar-week" role="row">
                  <For each={week}>
                    {(day) => (
                      <div
                        role="gridcell"
                        aria-selected={
                          day === draft().from ||
                          day === draft().to ||
                          !!(
                            draft().from &&
                            draft().to &&
                            day > draft().from &&
                            day < draft().to
                          )
                        }
                      >
                        <button
                          data-day={day}
                          tabIndex={focused() === day ? 0 : -1}
                          classList={{
                            "calendar-day": true,
                            outside: day.slice(0, 7) !== month().slice(0, 7),
                            endpoint:
                              day === draft().from || day === draft().to,
                            "in-range": !!(
                              draft().from &&
                              draft().to &&
                              day > draft().from &&
                              day < draft().to
                            ),
                          }}
                          aria-label={dayDate(day).toLocaleDateString(
                            undefined,
                            { dateStyle: "full", timeZone: "UTC" },
                          )}
                          aria-current={day === utcDay() ? "date" : undefined}
                          onKeyDown={(event) => calendarKey(event, day)}
                          onClick={() => selectDay(day)}
                        >
                          {Number(day.slice(8))}
                        </button>
                      </div>
                    )}
                  </For>
                </div>
              )}
            </For>
          </div>
          <p id={`${id}-help`} class="footnote" role="status">
            {rangeError(draft()) ||
              `Choose ${selecting() === "from" ? "a start" : "an end"} date. Leave either field blank for an open-ended range.`}
          </p>
          <div class="date-actions">
            <button
              class="text-button"
              onClick={() => apply({ from: "", to: "" })}
            >
              All dates
            </button>
            <span class="spacer" />
            <button onClick={() => close()}>Cancel</button>
            <button
              class="primary"
              disabled={!!rangeError(draft())}
              onClick={() => apply(draft())}
            >
              Apply
            </button>
          </div>
        </div>
      </Show>
    </div>
  );
}
