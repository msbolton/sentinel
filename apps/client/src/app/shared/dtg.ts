const MONTHS = ['JAN', 'FEB', 'MAR', 'APR', 'MAY', 'JUN', 'JUL', 'AUG', 'SEP', 'OCT', 'NOV', 'DEC'];

const pad2 = (n: number): string => String(n).padStart(2, '0');

/** A military date-time group in UTC: day, hour, minute, `Z`, month, two-digit year. */
export function formatDtg(date: Date): string {
  const day = pad2(date.getUTCDate());
  const hour = pad2(date.getUTCHours());
  const minute = pad2(date.getUTCMinutes());
  const month = MONTHS[date.getUTCMonth()];
  const year = pad2(date.getUTCFullYear() % 100);
  return `${day}${hour}${minute}Z ${month} ${year}`;
}
