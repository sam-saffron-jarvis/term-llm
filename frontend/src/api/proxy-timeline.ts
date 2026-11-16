export type ChartRange = 'today' | '7d' | '30d';
export type ChartGroup = 'total' | 'client' | 'provider';
export interface TimelineSeries {
  key: string;
  label: string;
  costs: { currency: string; micros: number; estimated: boolean }[];
  tokens?: number;
  unknown_cost: number;
  unknown_tokens: number;
  plan: number;
}
export interface TimelineBucket {
  start: string;
  end: string;
  label: string;
  series: TimelineSeries[];
}
export interface MoneyTimeline {
  timezone: string;
  range: ChartRange;
  start: string;
  end: string;
  currencies: string[];
  buckets: TimelineBucket[];
}
