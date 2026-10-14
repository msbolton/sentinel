export enum LocationCategory {
  CITY = 'CITY',
  MILITARY_BASE = 'MILITARY_BASE',
  PORT = 'PORT',
  AIRPORT = 'AIRPORT',
  CUSTOM = 'CUSTOM',
}

export interface Location {
  id: string;
  name: string;
  description?: string;
  latitude: number;
  longitude: number;
  altitude: number;
  heading: number;
  pitch: number;
  range: number;
  has3dTiles: boolean;
  category: LocationCategory;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}
