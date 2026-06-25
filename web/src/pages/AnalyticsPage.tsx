import { Link, useParams } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';
import { ClicksChart } from '../components/ClicksChart';
import { useStats } from '../hooks/useLinks';

export const AnalyticsPage = () => {
  const { code } = useParams<{ code: string }>();
  const { data, isLoading, error } = useStats(code ?? '');

  const backLink = (
    <Link
      to="/links"
      className="inline-flex items-center gap-1 text-sm font-medium text-blue-700 hover:underline dark:text-blue-400"
    >
      <ArrowLeft size={16} /> Back to links
    </Link>
  );

  if (!code)
    return (
      <section className="space-y-4">
        <p role="alert" className="text-red-600 dark:text-red-400">
          No link code in the URL.
        </p>
        {backLink}
      </section>
    );

  return (
    <section className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-slate-900 dark:text-slate-100">Analytics</h2>
          <p className="mt-1 font-mono text-sm text-slate-500 dark:text-slate-400">/{code}</p>
        </div>
        {backLink}
      </div>

      {isLoading && (
        <div className="rounded-xl border border-slate-200 bg-white p-8 text-center text-slate-500 shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400">
          Loading stats…
        </div>
      )}

      {error && (
        <div
          role="alert"
          className="rounded-xl border border-red-200 bg-white p-8 text-center text-red-600 shadow-sm dark:border-red-900 dark:bg-slate-900 dark:text-red-400"
        >
          {error.message}
        </div>
      )}

      {data && (
        <>
          <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
            <p className="text-sm font-medium text-slate-500 dark:text-slate-400">Total clicks</p>
            <p className="mt-1 text-4xl font-bold text-slate-900 dark:text-slate-100">
              {data.total}
            </p>
          </div>
          <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
            <p className="mb-4 text-sm font-medium text-slate-500 dark:text-slate-400">
              Clicks over time
            </p>
            <ClicksChart series={data.series ?? []} />
          </div>
        </>
      )}
    </section>
  );
};
