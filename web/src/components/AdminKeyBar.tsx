import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { KeyRound } from 'lucide-react';
import { getAdminKey, setAdminKey } from '../adminKey';

// Lets the operator paste their admin key so the Manage page can list/delete.
// Saving stores it locally and re-runs the links query (which now sends the key).
export const AdminKeyBar = () => {
  const queryClient = useQueryClient();
  const [value, setValue] = useState(getAdminKey());

  const save = () => {
    setAdminKey(value);
    queryClient.invalidateQueries({ queryKey: ['links'] });
  };

  const saved = getAdminKey() !== '' && getAdminKey() === value.trim();

  return (
    <div className="mb-6 flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-slate-900">
      <KeyRound size={16} className="shrink-0 text-slate-400" />
      <input
        type="password"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="Paste your admin key to manage links"
        className="flex-1 bg-transparent text-sm text-slate-900 outline-none placeholder:text-slate-400 dark:text-slate-100"
      />
      <span
        className={`text-xs ${saved ? 'text-green-600 dark:text-green-400' : 'text-slate-400'}`}
      >
        {saved ? 'saved' : 'not saved'}
      </span>
      <button
        type="button"
        onClick={save}
        className="rounded-md bg-blue-600 px-3 py-1 text-sm font-medium text-white transition-colors hover:bg-blue-700"
      >
        Save
      </button>
    </div>
  );
};
