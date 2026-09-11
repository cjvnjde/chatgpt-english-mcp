import type { AnalyticsData } from "./types";

export function activityDays(
  activity: AnalyticsData["activity"],
  now = new Date(),
) {
  return Array.from({ length: 30 }, (_, i) => {
    const date = new Date(now);
    date.setUTCDate(date.getUTCDate() - 29 + i);
    const day = date.toISOString().slice(0, 10);
    return (
      activity.find((entry) => entry.day === day) || {
        day,
        reviews: 0,
        recalled: 0,
      }
    );
  });
}

export function wordsNeedingAttention(words: AnalyticsData["difficult"]) {
  return words.filter(
    (word) => Number(word.lapses) > 0 || Number(word.consecutive_failures) > 0,
  );
}
