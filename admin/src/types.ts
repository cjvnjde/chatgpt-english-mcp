export type Row = Record<string, unknown>;
export type Column = {
  name: string;
  type: string;
  notNull: boolean;
  primaryKey: boolean;
};
export type Table = {
  name: string;
  sql: string;
  columns: Column[];
  count: number;
};
export type Page = {
  rows: Row[];
  total: number;
  limit: number;
  offset: number;
};
export type Session = { owner: string; version: number };
export type Vocabulary = {
  itemId: string;
  revision: number;
  term: string;
  normalizedTerm: string;
  context?: string;
  status: string;
  usefulness: string;
  personalInterest: "low" | "normal" | "high";
  tags: string[];
  notes: string[];
  examples: string[];
  customDescription?: string;
  descriptionSource?: { title?: string; url?: string };
  sense?: Row;
  lookup?: Row;
  createdAt: string;
  updatedAt: string;
};
export type AnalyticsData = {
  owner: string;
  statuses: { label: string; count: number }[];
  ratings: { label: string; count: number }[];
  effectiveRatings: { label: string; count: number }[];
  activity: { day: string; reviews: number; recalled: number }[];
  due: { count: number }[];
  comments: { count: number }[];
  difficult: Row[];
};
export type Suggestion = {
  vocabularyItemId: string;
  cardId: string;
  term: string;
  context?: string;
  status: string;
  usefulness: string;
  dueAt: string;
  lastShownAt?: string;
  pool: "new" | "learning" | "review";
  probability: number;
  reason:
    | "new"
    | "due"
    | "early"
    | "cooldown"
    | "learning_first"
    | "not_due"
    | "waiting";
};
export type SuggestionsPage = {
  owner: string;
  generatedAt: string;
  total: number;
  selectable: number;
  limit: number;
  offset: number;
  rows: Suggestion[];
};
