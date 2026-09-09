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
  term: string;
  normalizedTerm: string;
  context?: string;
  status: string;
  usefulness: string;
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
