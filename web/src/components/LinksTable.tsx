import { useState } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { BarChart3, ChevronLeft, ChevronRight, Link2, Trash2 } from 'lucide-react';
import type { LinkSummary } from '../types';
import { useDeleteLink, useLinks } from '../hooks/useLinks';

const PAGE_SIZE = 20;

export const LinksTable = () => {
  const [page, setPage] = useState(0); // zero-based page index
  const offset = page * PAGE_SIZE;
  const { data, isLoading, error, isPlaceholderData } = useLinks(PAGE_SIZE, offset);

  if (isLoading) return <Card center>Loading links…</Card>;
  if (error)
    return (
      <Card center tone="error">
        {error.message}
      </Card>
    );

  const links = data?.links ?? [];
  if (links.length === 0 && page === 0)
    return (
      <Card center>
        <Link2 className="mx-auto mb-3 text-slate-400 dark:text-slate-600" size={32} />
        No links yet — create one to get started.
      </Card>
    );

  // We don't know the total count, so a full page means there's probably another.
  const hasNext = links.length === PAGE_SIZE;

  return (
    <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-slate-200 bg-slate-50 text-xs tracking-wide text-slate-500 uppercase dark:border-slate-800 dark:bg-slate-800/50 dark:text-slate-400">
            <th className="px-5 py-3 font-medium">Short</th>
            <th className="px-5 py-3 font-medium">Destination</th>
            <th className="px-5 py-3 font-medium">Created</th>
            <th className="px-5 py-3 text-right font-medium">Actions</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
          {links.map((link) => (
            <LinkRow key={link.code} link={link} />
          ))}
        </tbody>
      </table>

      <div className="flex items-center justify-between border-t border-slate-200 bg-slate-50 px-5 py-3 text-sm text-slate-600 dark:border-slate-800 dark:bg-slate-800/50 dark:text-slate-400">
        <span>
          {links.length > 0 ? `Showing ${offset + 1}–${offset + links.length}` : 'No more links'}
        </span>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={page === 0 || isPlaceholderData}
            className="inline-flex items-center gap-1 rounded-md border border-slate-300 bg-white px-3 py-1 font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            <ChevronLeft size={16} /> Prev
          </button>
          <button
            type="button"
            onClick={() => setPage((p) => p + 1)}
            disabled={!hasNext || isPlaceholderData}
            className="inline-flex items-center gap-1 rounded-md border border-slate-300 bg-white px-3 py-1 font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            Next <ChevronRight size={16} />
          </button>
        </div>
      </div>
    </div>
  );
};

// Each row owns its delete mutation so the "Deleting…" state stays on the
// clicked row only — a single shared mutation would disable every row's button.
const LinkRow = ({ link }: { link: LinkSummary }) => {
  const del = useDeleteLink();

  return (
    <tr className="hover:bg-slate-50 dark:hover:bg-slate-800/50">
      <td className="px-5 py-3">
        <a
          href={link.short_url}
          target="_blank"
          rel="noreferrer"
          className="font-mono text-blue-700 hover:underline dark:text-blue-400"
        >
          /{link.code}
        </a>
      </td>
      <td
        className="max-w-xs truncate px-5 py-3 text-slate-600 dark:text-slate-400"
        title={link.long_url}
      >
        {link.long_url}
      </td>
      <td className="px-5 py-3 whitespace-nowrap text-slate-500 dark:text-slate-500">
        {new Date(link.created_at).toLocaleDateString()}
      </td>
      <td className="px-5 py-3">
        <div className="flex items-center justify-end gap-1">
          <Link
            to={`/analytics/${link.code}`}
            title="Analytics"
            aria-label="Analytics"
            className="rounded-md p-1.5 text-slate-500 hover:bg-blue-50 hover:text-blue-700 dark:text-slate-400 dark:hover:bg-blue-950 dark:hover:text-blue-300"
          >
            <BarChart3 size={16} />
          </Link>
          <button
            type="button"
            onClick={() => del.mutate(link.code)}
            disabled={del.isPending}
            title="Delete"
            aria-label="Delete"
            className="rounded-md p-1.5 text-slate-500 hover:bg-red-50 hover:text-red-700 disabled:opacity-50 dark:text-slate-400 dark:hover:bg-red-950 dark:hover:text-red-400"
          >
            <Trash2 size={16} />
          </button>
        </div>
        {del.error && (
          <p className="mt-1 text-right text-xs text-red-600 dark:text-red-400">
            {del.error.message}
          </p>
        )}
      </td>
    </tr>
  );
};

// Shared card shell for the table's loading / error / empty states.
const Card = ({
  children,
  center,
  tone,
}: {
  children: ReactNode;
  center?: boolean;
  tone?: 'error';
}) => (
  <div
    className={`rounded-xl border border-slate-200 bg-white p-8 shadow-sm dark:border-slate-800 dark:bg-slate-900 ${center ? 'text-center' : ''}`}
  >
    <div
      className={
        tone === 'error' ? 'text-red-600 dark:text-red-400' : 'text-slate-500 dark:text-slate-400'
      }
      role={tone === 'error' ? 'alert' : undefined}
    >
      {children}
    </div>
  </div>
);
