import { Link, NavLink, Route, Routes } from 'react-router-dom';
import { Link2, Moon, Sun } from 'lucide-react';
import { toggleTheme, useTheme } from './theme';
import { AnalyticsPage } from './pages/AnalyticsPage';
import { CreatePage } from './pages/CreatePage';
import { LinksPage } from './pages/LinksPage';

const navClass = ({ isActive }: { isActive: boolean }) =>
  [
    'rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
    isActive
      ? 'bg-blue-50 text-blue-700 dark:bg-blue-950 dark:text-blue-300'
      : 'text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-100',
  ].join(' ');

const ThemeToggle = () => {
  const isDark = useTheme() === 'dark';
  return (
    <button
      type="button"
      onClick={toggleTheme}
      aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
      title={isDark ? 'Light mode' : 'Dark mode'}
      className="rounded-md p-2 text-slate-600 transition-colors hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-100"
    >
      {isDark ? <Sun size={18} /> : <Moon size={18} />}
    </button>
  );
};

export const App = () => (
  <div className="min-h-screen">
    <header className="sticky top-0 z-10 border-b border-slate-200 bg-white/80 backdrop-blur dark:border-slate-800 dark:bg-slate-900/80">
      <div className="mx-auto flex max-w-4xl items-center justify-between px-6 py-3">
        <Link
          to="/"
          className="flex items-center gap-2 text-lg font-bold text-slate-900 dark:text-slate-100"
        >
          <Link2 className="text-blue-600" size={20} /> shortn
        </Link>
        <nav className="flex items-center gap-1">
          <NavLink to="/" end className={navClass}>
            Create
          </NavLink>
          <NavLink to="/links" className={navClass}>
            Links
          </NavLink>
          <ThemeToggle />
        </nav>
      </div>
    </header>

    <main className="mx-auto max-w-4xl px-6 py-10">
      <Routes>
        <Route path="/" element={<CreatePage />} />
        <Route path="/links" element={<LinksPage />} />
        <Route path="/analytics/:code" element={<AnalyticsPage />} />
      </Routes>
    </main>
  </div>
);
