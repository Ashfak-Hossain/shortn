import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createLink, deleteLink, getStats, listLinks } from '../api/links';

// base key; per-page queries extend it. invalidating ['links'] matches every
// page by prefix, so a create/delete refreshes whatever page you're on.
const linksKey = ['links'] as const;

export const useLinks = (limit: number, offset: number) =>
  useQuery({
    queryKey: [...linksKey, { limit, offset }],
    queryFn: () => listLinks(limit, offset),
    // keep the current page on screen while the next one loads (no flash to empty)
    placeholderData: keepPreviousData,
  });

export const useCreateLink = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (url: string) => createLink(url),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: linksKey }),
  });
};

export const useDeleteLink = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (code: string) => deleteLink(code),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: linksKey }),
  });
};

// an empty code has no link to fetch stats for, so skip the request entirely
export const useStats = (code: string) =>
  useQuery({
    queryKey: ['stats', code],
    queryFn: () => getStats(code),
    enabled: code.length > 0,
  });
