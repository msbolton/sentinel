import { Alert } from './models/alert.model';

/**
 * Fold one incoming alert into the ticker list. An acknowledged alert leaves
 * the list; any other goes to the front, replacing an earlier copy of itself,
 * and the list is cut to `max`. Returns a new array.
 */
export function pushAlert(list: Alert[], alert: Alert, max = 5): Alert[] {
  const rest = list.filter((a) => a.id !== alert.id);
  if (alert.acknowledged) return rest;
  return [alert, ...rest].slice(0, max);
}
