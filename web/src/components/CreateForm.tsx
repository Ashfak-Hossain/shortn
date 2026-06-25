import { useState } from 'react';
import type { FormEvent } from 'react';
import { Check, Copy, ExternalLink } from 'lucide-react';
import { useCreateLink } from '../hooks/useLinks';

export const CreateForm = () => {
  const [url, setUrl] = useState('');
  const [copied, setCopied] = useState(false);
  const create = useCreateLink();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    setCopied(false);
    create.reset(); // drop the previous result/error before retrying
    create.mutate(url, { onSuccess: () => setUrl('') });
  };

  const copy = async (shortUrl: string) => {
    try {
      await navigator.clipboard.writeText(shortUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (insecure origin / denied) — leave the label as "Copy"
    }
  };

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <form onSubmit={handleSubmit} className="flex gap-2">
        <input
          type="url"
          required
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://example.com/some/long/path"
          className="flex-1 rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 placeholder:text-slate-400 focus:border-blue-500 focus:ring-2 focus:ring-blue-100 focus:outline-none dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:placeholder:text-slate-500 dark:focus:ring-blue-900"
        />
        <button
          type="submit"
          disabled={create.isPending}
          className="rounded-lg bg-blue-600 px-5 py-2 font-medium text-white transition-colors hover:bg-blue-700 disabled:opacity-50"
        >
          {create.isPending ? 'Shortening…' : 'Shorten'}
        </button>
      </form>

      {/* Server validates and blocks bad/internal URLs; surface its message verbatim. */}
      {create.error && (
        <p role="alert" className="mt-3 text-sm text-red-600 dark:text-red-400">
          {create.error.message}
        </p>
      )}

      {create.data && (
        <div className="mt-4 flex items-center gap-3 rounded-lg border border-green-200 bg-green-50 px-4 py-3 dark:border-green-900 dark:bg-green-950">
          <ExternalLink size={16} className="shrink-0 text-green-700 dark:text-green-400" />
          <a
            href={create.data.short_url}
            target="_blank"
            rel="noreferrer"
            className="flex-1 truncate font-mono text-blue-700 hover:underline dark:text-blue-400"
          >
            {create.data.short_url}
          </a>
          <button
            type="button"
            onClick={() => copy(create.data!.short_url)}
            className="inline-flex shrink-0 items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}
            {copied ? 'Copied!' : 'Copy'}
          </button>
        </div>
      )}
    </div>
  );
};
