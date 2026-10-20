/**
 * The text drawn beside an entity on the map: its name, an optional suffix
 * (a satellite's altitude), and ' · EX' when the entity is exercise data so
 * synthetic tracks cannot be mistaken for live ones.
 */
export function entityLabel(name: string, exercise: boolean | undefined, suffix?: string): string {
  let text = name;
  if (suffix) {
    text += ` · ${suffix}`;
  }
  if (exercise) {
    text += ' · EX';
  }
  return text;
}
