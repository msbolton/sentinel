import { readFileSync } from 'fs';
import { join } from 'path';
import { JsonSchema } from '../models/feed.model';

/** The drivers whose package directory is not their id. */
const DIRS: Record<string, string> = { grpc: 'grpcin' };

/**
 * The config schema an ingest driver really ships (`schema.json`), for specs that must fail when an edit to it is
 * one the client cannot honour. Not part of the app: only specs import it.
 */
export function schemaOf(driver: string): JsonSchema {
  const file = join(__dirname, '../../../../../ingest-service/internal/source/driver', DIRS[driver] ?? driver, 'schema.json');
  return JSON.parse(readFileSync(file, 'utf-8')) as JsonSchema;
}
