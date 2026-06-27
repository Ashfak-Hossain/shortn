// admin key lives in localStorage.
const STORAGE_KEY = 'shortn.adminKey';

export const getAdminKey = (): string => localStorage.getItem(STORAGE_KEY) ?? '';

export const setAdminKey = (value: string): void => {
  const trimmed = value.trim();
  if (trimmed) localStorage.setItem(STORAGE_KEY, trimmed);
  else localStorage.removeItem(STORAGE_KEY);
};
