/** Date helpers; the locale comes from the page language. */
export function formatDate(value: string | number | Date | null | undefined, lang: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat(lang, {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  }).format(new Date(value));
}

export function formatDateTime(
  value: string | number | Date | null | undefined,
  lang: string,
): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat(lang, {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value));
}

export function formatTime(value: string | number | Date, lang: string): string {
  return new Intl.DateTimeFormat(lang, { hour: '2-digit', minute: '2-digit' }).format(
    new Date(value),
  );
}

/** "today", "yesterday" or the date: for the day groups of the history. */
export function dayLabel(
  value: string | number | Date,
  lang: string,
  today: string,
  yesterday: string,
): string {
  const d = new Date(value);
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((startOf(new Date()) - startOf(d)) / 86_400_000);
  if (diff === 0) return today;
  if (diff === 1) return yesterday;
  return new Intl.DateTimeFormat(lang, { day: 'numeric', month: 'long', year: 'numeric' }).format(
    d,
  );
}

/** An ISO date (RFC 3339) from a yyyy-mm-dd field: the end of that day, local time. */
export function endOfDayISO(date: string): string {
  const [y, m, d] = date.split('-').map(Number);
  return new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1, 23, 59, 59).toISOString();
}
