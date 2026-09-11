const percent = new Intl.NumberFormat(undefined, {
  style: "percent",
  maximumFractionDigits: 2,
});
export function chance(probability: number) {
  return probability > 0 && probability < 0.0001
    ? `<${percent.format(0.0001)}`
    : percent.format(probability);
}
// Bars compare the current page only; the visible percentage stays absolute.
export function chanceWidth(probability: number, pageMaximum: number) {
  return pageMaximum > 0
    ? Math.max(0, Math.min(100, (probability / pageMaximum) * 100))
    : 0;
}
