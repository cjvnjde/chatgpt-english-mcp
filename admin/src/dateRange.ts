export type DateRange = { from: string; to: string };
const dayMS = 86_400_000;
export const utcDay = (date = new Date()) => date.toISOString().slice(0, 10);
export const dayDate = (day: string) => new Date(`${day}T00:00:00Z`);
export function validDay(day: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return false;
  const date = dayDate(day);
  return Number.isFinite(date.getTime()) && utcDay(date) === day;
}
export function shiftDay(day: string, amount: number) {
  return utcDay(new Date(dayDate(day).getTime() + amount * dayMS));
}
export function shiftMonth(day: string, amount: number) {
  const date = dayDate(day);
  date.setUTCDate(1);
  date.setUTCMonth(date.getUTCMonth() + amount);
  return utcDay(date);
}
export function calendarDays(month: string) {
  const first = `${month.slice(0, 7)}-01`;
  const start = shiftDay(first, -(dayDate(first).getUTCDay() + 6) % 7);
  return Array.from({ length: 42 }, (_, i) => shiftDay(start, i));
}
export function recentRange(days: number, now = new Date()): DateRange {
  const to = utcDay(now);
  return { from: shiftDay(to, 1 - days), to };
}
export function rangeError(range: DateRange) {
  if (
    (range.from && !validDay(range.from)) ||
    (range.to && !validDay(range.to))
  )
    return "Enter valid dates in YYYY-MM-DD format.";
  if (range.from && range.to && range.from > range.to)
    return "The end date must be on or after the start date.";
  return "";
}
export function rangeLabel(range: DateRange) {
  const format = (day: string) =>
    validDay(day)
      ? dayDate(day).toLocaleDateString(undefined, {
          month: "short",
          day: "numeric",
          year: "numeric",
          timeZone: "UTC",
        })
      : day;
  if (range.from && range.to)
    return range.from === range.to
      ? format(range.from)
      : `${format(range.from)} – ${format(range.to)}`;
  if (range.from) return `Since ${format(range.from)}`;
  if (range.to) return `Through ${format(range.to)}`;
  return "All dates";
}
