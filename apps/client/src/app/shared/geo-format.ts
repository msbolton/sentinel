/** A coordinate as an operator reads it: four decimals and a hemisphere letter. */
export function formatCoordinate(value: number, axis: 'lat' | 'lon'): string {
  const hemisphere = axis === 'lat' ? (value < 0 ? 'S' : 'N') : value < 0 ? 'W' : 'E';
  return `${Math.abs(value).toFixed(4)}° ${hemisphere}`;
}
