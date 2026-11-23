const formatters = new Map<number, Intl.NumberFormat>();
const timeFormatter = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
});

export function fmt(value: string, dp: number): string {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return '—';
  let formatter = formatters.get(dp);
  if (!formatter) {
    formatter = new Intl.NumberFormat('en-US', { minimumFractionDigits: dp, maximumFractionDigits: dp });
    formatters.set(dp, formatter);
  }
  return formatter.format(numeric);
}

export function fmtTime(iso: string): string {
  const milliseconds = Date.parse(iso);
  return Number.isFinite(milliseconds) ? timeFormatter.format(milliseconds) : '—';
}

export function changeTone(value: string): string {
  const numeric = Number(value);
  return numeric > 0 ? 'rise' : numeric < 0 ? 'fall' : '';
}

export function fmtChange(value: string): string {
  return `${Number(value) >= 0 ? '+' : ''}${fmt(value, 2)}%`;
}
