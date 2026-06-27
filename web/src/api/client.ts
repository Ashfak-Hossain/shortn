import { getAdminKey } from '../adminKey';

// Base URL is baked in at build time. Empty string = same origin: in prod that's behind the ingress;
const BASE_URL = import.meta.env.VITE_API_BASE_URL ?? '';

// Carries the HTTP status + the server's { error } message
export class ApiRequestError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiRequestError';
    this.status = status;
  }
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // The admin key (if the operator has set one) rides along on every request as
  // X-Admin-Key. The public endpoints ignore it; only list/delete require it.
  const adminKey = getAdminKey();
  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(adminKey ? { 'X-Admin-Key': adminKey } : {}),
      ...init?.headers,
    },
  });

  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // non-JSON error body — keep the status text
    }
    throw new ApiRequestError(res.status, message);
  }

  if (res.status === 204) return undefined as T;

  return (await res.json()) as T;
}
