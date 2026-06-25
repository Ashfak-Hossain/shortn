import { useSyncExternalStore } from 'react';

type Theme = 'light' | 'dark';

// The theme lives as a `dark` class on <html>, applied before paint by the inline
// script in index.html (so there's no flash of the wrong theme on load). This tiny
// store just lets React read that class and re-render when the toggle flips it.
const listeners = new Set<() => void>();

const getTheme = (): Theme =>
  document.documentElement.classList.contains('dark') ? 'dark' : 'light';

const subscribe = (cb: () => void) => {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
};

export const toggleTheme = () => {
  const next: Theme = getTheme() === 'dark' ? 'light' : 'dark';
  document.documentElement.classList.toggle('dark', next === 'dark');
  try {
    localStorage.setItem('theme', next);
  } catch {
    // ignore storage failures (private mode, etc.) — the class still flips for this session
  }
  listeners.forEach((cb) => cb());
};

export const useTheme = (): Theme => useSyncExternalStore(subscribe, getTheme, () => 'light');
