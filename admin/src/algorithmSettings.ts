export const settingGroups: { title: string; help: string; fields: SettingField[] }[] = [
  { title: "Review scheduling", help: "Controls future reviews. Existing due dates and review history are not recalculated.", fields: [
    { key: "fastAnswerSeconds", label: "Fast-answer threshold", unit: "seconds", min: 0, max: 300, integer: true, help: "Promote Good to Easy only before this threshold, measured from the token’s first server presentation to review receipt. 0 disables promotion; slow answers and other ratings never change. Explicit corrections bypass promotion." },
    { key: "requestedRetention", label: "Target retention", unit: "%", min: 70, max: 99, percent: true, help: "Desired probability of remembering at the next scheduled review. Higher targets generally mean more reviews." },
    { key: "maximumIntervalDays", label: "Maximum review interval", unit: "days", min: 1, max: 36500, integer: true, help: "Upper limit for newly scheduled review intervals." },
    { key: "learningStepsMinutes", label: "Learning steps", unit: "minutes", list: true, help: "Comma-separated increasing positive minutes, each below 1440; at most 10. Leave empty to skip steps." },
    { key: "relearningStepsMinutes", label: "Relearning steps", unit: "minutes", list: true, help: "Steps after forgetting a learned word. Same limits as learning steps; empty skips steps." },
  ] },
  { title: "Mastery", help: "Thresholds for marking a word learned on future reviews.", fields: [
    { key: "masteryDays", label: "Successful review days", unit: "days", min: 1, max: 365, integer: true, help: "Required successful UTC review days without a Hard or Again reset. At most one credit per day." },
    { key: "masteryIntervalDays", label: "Mastery interval", unit: "days", min: 1, max: 36500, integer: true, help: "Required scheduled interval; cannot exceed the maximum review interval." },
  ] },
  { title: "Selection mix and recency", help: "Balances new, active and learned words in future suggestions.", fields: [
    { key: "presentationCooldownMinutes", label: "Presentation cooldown", unit: "minutes", min: 0, max: 1440, integer: true, help: "Exclude recent presentations only within this window. Small pools may relax exclusions to avoid immediate repeats. 0 disables cooldown." },
    { key: "recentPresentationCount", label: "Recent presentation exclusion", unit: "presentations", min: 0, max: 100, integer: true, help: "Recent presentations considered for cooldown, together with the time window. 0 disables cooldown." },
    { key: "newShareMin", label: "Minimum new-word share", unit: "%", min: 1, max: 99, percent: true, help: "Lower bound within active work when both new and due review pools are available. Due learning steps take priority." },
    { key: "newShareMax", label: "Maximum new-word share", unit: "%", min: 1, max: 99, percent: true, help: "Upper bound within active work; must be at least the minimum. Not a quota when only one pool is available." },
    { key: "learnedShareMin", label: "Minimum learned-word share", unit: "%", min: 0, max: 99, percent: true, help: "Lower bound when both active and due learned words are available, after prioritizing learning steps." },
    { key: "learnedShareMax", label: "Maximum learned-word share", unit: "%", min: 0, max: 99, percent: true, help: "Upper bound when competing with active words; must be at least the minimum. Learned-only work can use the whole draw." },
    { key: "learnedWeightDivisor", label: "Learned-word weight divisor", unit: "divisor", min: 1, max: 100, help: "Learned share starts at learned mass / (divisor × active mass + learned mass). Higher values favor active words." },
  ] },
  { title: "Priority and exposure weights", help: "Multipliers affect selection probability, not review grades. Normal usefulness and interest retain a weight of 1.", fields: [
    { key: "lowUsefulnessWeight", label: "Low usefulness", unit: "×", min: 0.05, max: 20, help: "Weight for low-usefulness words in new-card selection and reinforcement." },
    { key: "highUsefulnessWeight", label: "High usefulness", unit: "×", min: 0.05, max: 20, help: "Weight for high-usefulness words in new-card selection and reinforcement." },
    { key: "lowInterestWeight", label: "Low personal interest", unit: "×", min: 0.05, max: 20, help: "Weight for words marked low personal interest." },
    { key: "highInterestWeight", label: "High personal interest", unit: "×", min: 0.05, max: 20, help: "Weight for words marked high personal interest." },
    { key: "unseenExposureWeight", label: "Unseen-word exposure", unit: "×", min: 0.05, max: 20, help: "Exposure multiplier for words not yet presented." },
    { key: "exposureRecoveryHours", label: "Exposure recovery", unit: "hours", min: 1, max: 168, help: "Time for exposure weight to recover from 0.25× to 1×. The later rise to 2× remains over 29 days." },
  ] },
  { title: "Reinforcement and troublesome words", help: "Controls reinforcement eligibility and troublesome-word suggestions.", fields: [
    { key: "reinforcementCooldownHours", label: "Reinforcement cooldown", unit: "hours", min: 0, max: 720, help: "Wait before reinforcing the same word again. 0 disables the cooldown." },
    { key: "reinforcementMaxWordShare", label: "Maximum reinforcement word share", unit: "%", min: 5, max: 100, percent: true, help: "Caps one word’s probability. Requires at least max(4, ceiling(100 / share percentage)) eligible words." },
    { key: "troublesomeConsecutiveFailures", label: "Consecutive failure threshold", unit: "failures", min: 1, max: 100, integer: true, help: "Consecutive failures needed to flag a troublesome word." },
    { key: "troublesomeLapses", label: "Lapse threshold", unit: "lapses", min: 1, max: 100, integer: true, help: "Total lapses needed to flag a troublesome word." },
  ] },
];

export type SettingField = { key: string; label: string; unit: string; help: string; min?: number; max?: number; integer?: boolean; percent?: boolean; list?: boolean };
export const settingFields: SettingField[] = settingGroups.flatMap(group => group.fields);
export type SettingsValues = Record<string, number | number[]>;
export type SettingsSnapshot = { values: SettingsValues; revision: number; defaults: SettingsValues };
export type SettingsDraft = Record<string, string>;
export function settingsDraft(values: SettingsValues): SettingsDraft {
  return Object.fromEntries(settingFields.map(field => {
    const value = values[field.key];
    return [field.key, Array.isArray(value) ? value.join(", ") : String(field.percent ? Number((value * 100).toPrecision(15)) : value)];
  }));
}
export function validateSettings(draft: SettingsDraft) {
  const values: SettingsValues = {};
  const errors: Record<string, string> = {};
  for (const field of settingFields) {
    const text = (draft[field.key] ?? "").trim();
    if (field.list) {
      const parts = text ? text.split(",").map(part => part.trim()) : [];
      const steps = parts.map(Number);
      if (parts.some(part => !part) || steps.length > 10 || steps.some((step, index) => !Number.isFinite(step) || step <= 0 || step >= 1440 || (index > 0 && step <= steps[index - 1]))) errors[field.key] = "Enter up to 10 increasing minute values greater than 0 and below 1440, or leave empty.";
      values[field.key] = steps;
    } else {
      const value = Number(text);
      if (!text || !Number.isFinite(value) || value < field.min! || value > field.max! || (field.integer && !Number.isInteger(value))) errors[field.key] = `Enter ${field.integer ? "a whole number" : "a number"} from ${field.min} to ${field.max} ${field.unit}.`;
      values[field.key] = field.percent ? value / 100 : value;
    }
  }
  for (const [min, max] of [["newShareMin", "newShareMax"], ["learnedShareMin", "learnedShareMax"], ["masteryIntervalDays", "maximumIntervalDays"]]) {
    if (values[min] > values[max]) errors[min] = min === "masteryIntervalDays" ? "Mastery interval cannot exceed the maximum review interval." : "Minimum share cannot exceed maximum share.";
  }
  return { values, errors };
}
