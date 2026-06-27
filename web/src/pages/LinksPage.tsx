import { LinksTable } from '../components/LinksTable';
import { AdminKeyBar } from '../components/AdminKeyBar';

export const LinksPage = () => (
  <section>
    <h2 className="text-2xl font-bold text-slate-900 dark:text-slate-100">Your links</h2>
    <p className="mt-1 mb-6 text-sm text-slate-500 dark:text-slate-400">
      Manage your short links and open their analytics.
    </p>
    <AdminKeyBar />
    <LinksTable />
  </section>
);
