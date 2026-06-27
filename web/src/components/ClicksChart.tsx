import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { ClickBucket } from '../types';
import { useTheme } from '../theme';

// bucket is an RFC3339 hour start; show a compact month/day + hour label.
// Typed loose because recharts hands the Tooltip label back as ReactNode.
const formatBucket = (bucket: unknown) =>
  new Date(String(bucket)).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
  });

export const ClicksChart = ({ series }: { series: ClickBucket[] }) => {
  // Recharts styles its SVG via props, not CSS, so theme colors are picked here.
  const dark = useTheme() === 'dark';
  const axis = dark ? '#94a3b8' : '#6b7280'; // slate-400 / gray-500
  const grid = dark ? '#1e293b' : '#e5e7eb'; // slate-800 / gray-200

  if (series.length === 0)
    return (
      <p className="text-slate-500 dark:text-slate-400">
        No clicks yet — share the link to see traffic here.
      </p>
    );

  return (
    // ResponsiveContainer fills its parent, so the parent must set a real height.
    <div className="h-72 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={series} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke={grid} />
          <XAxis
            dataKey="bucket"
            tickFormatter={formatBucket}
            tick={{ fontSize: 12, fill: axis }}
          />
          <YAxis allowDecimals={false} tick={{ fontSize: 12, fill: axis }} />
          <Tooltip
            labelFormatter={formatBucket}
            contentStyle={
              dark
                ? {
                    backgroundColor: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: 8,
                    color: '#e2e8f0',
                  }
                : { borderRadius: 8 }
            }
            labelStyle={dark ? { color: '#94a3b8' } : undefined}
          />
          <Line type="monotone" dataKey="count" stroke="#3b82f6" strokeWidth={2} dot={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
};
