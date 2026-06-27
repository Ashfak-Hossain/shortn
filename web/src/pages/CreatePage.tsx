import { CreateForm } from '../components/CreateForm';

export const CreatePage = () => (
  <section>
    <h2 className="text-2xl font-bold text-slate-900 dark:text-slate-100">Create a short link</h2>
    <p className="mt-1 mb-6 text-sm text-slate-500 dark:text-slate-400">
      Paste a long URL and get a short, shareable link.
    </p>
    <CreateForm />
  </section>
);
