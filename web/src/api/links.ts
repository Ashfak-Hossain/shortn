import { request } from './client';
import type { CreateLinkResponse, ListResponse, Stats } from '../types';

export function createLink(url: string): Promise<CreateLinkResponse> {
  return request<CreateLinkResponse>('/api/links', {
    method: 'POST',
    body: JSON.stringify({ url }),
  });
}

export function listLinks(limit: number, offset: number): Promise<ListResponse> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
  return request<ListResponse>(`/api/links?${params}`);
}

export function deleteLink(code: string): Promise<void> {
  return request<void>(`/api/links/${encodeURIComponent(code)}`, { method: 'DELETE' });
}

export function getStats(code: string): Promise<Stats> {
  return request<Stats>(`/api/links/${encodeURIComponent(code)}/stats`);
}
