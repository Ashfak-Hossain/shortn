// These mirror the API's JSON exactly — snake_case field names match the Go
// response structs.

export interface CreateLinkResponse {
  code: string;
  short_url: string;
  long_url: string;
}

export interface LinkSummary {
  code: string;
  short_url: string;
  long_url: string;
  created_at: string; // RFC3339 timestamp (Go time.Time)
}

export interface ListResponse {
  links: LinkSummary[];
}

export interface ClickBucket {
  bucket: string; // start of the hour window, RFC3339
  count: number;
}

export interface Stats {
  code: string;
  total: number;
  series: ClickBucket[];
}
