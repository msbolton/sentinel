import { readFileSync } from 'fs';
import { join } from 'path';
import { JsonSchema } from '../../models/feed.model';
import { COMMON_OPTIONS_SCHEMA } from './common-options';

/** The common options the ingest service really validates every driver config against. */
const server: JsonSchema = JSON.parse(
  readFileSync(join(__dirname, '../../../../../../ingest-service/internal/drivers/schemas/common.json'), 'utf-8'),
);

/** Options of common.json that the connection form owns, so the schema form builds no field for them. */
const CONNECTION_FORM_OWNS = ['classification', 'exercise', 'military', 'direction', 'policy'];

/** Keywords that carry wording or structure rather than a limit: the copy may word an option its own way. */
const NOT_LIMITS = ['$schema', '$id', '$comment', 'title', 'description', 'examples', 'properties', 'additionalProperties'];

type Options = Record<string, Record<string, unknown>>;

/** `schema` and every property under it, by path (`/outbound/queueMax`), each with the keywords `keep` accepts. */
function flatten(schema: JsonSchema, keep: (keyword: string) => boolean, path = ''): Options {
  const own = Object.entries(schema).filter(([keyword, value]) => keep(keyword) && value !== undefined);
  const out: Options = { [path || '/']: Object.fromEntries(own) };
  for (const [key, prop] of Object.entries(schema.properties ?? {})) {
    Object.assign(out, flatten(prop, keep, `${path}/${key}`));
  }
  return out;
}

/** common.json as the schema form sees it: without the options the connection form owns. */
function serverOptions(keep: (keyword: string) => boolean): Options {
  const owned = (path: string) => CONNECTION_FORM_OWNS.some((key) => path === `/${key}` || path.startsWith(`/${key}/`));
  return Object.fromEntries(Object.entries(flatten(server, keep)).filter(([path]) => !owned(path)));
}

describe('COMMON_OPTIONS_SCHEMA against common.json', () => {
  it('has the options of common.json, bar the ones the connection form owns', () => {
    // an owned option that common.json no longer has is a stale exemption
    expect(Object.keys(server.properties ?? {})).toEqual(expect.arrayContaining(CONNECTION_FORM_OWNS));

    const none = () => false;
    expect(Object.keys(flatten(COMMON_OPTIONS_SCHEMA, none)).sort()).toEqual(Object.keys(serverOptions(none)).sort());
  });

  it('closes the groups to extra keys that common.json closes', () => {
    const closing = (keyword: string) => keyword === 'additionalProperties';
    expect(flatten(COMMON_OPTIONS_SCHEMA, closing)).toEqual(serverOptions(closing));
  });

  it('limits and defaults each option as common.json does', () => {
    const limit = (keyword: string) => !NOT_LIMITS.includes(keyword);
    expect(flatten(COMMON_OPTIONS_SCHEMA, limit)).toEqual(serverOptions(limit));
  });
});
