import { JsonSchema } from '../../models/feed.model';

/**
 * Runtime options every driver accepts alongside its own configuration.
 * Mirrors apps/ingest-service/internal/drivers/schemas/common.json; the
 * values land in the feed's config next to the driver's own keys.
 */
export const COMMON_OPTIONS_SCHEMA: JsonSchema = {
  title: 'Common feed options',
  description: 'Runtime options every driver accepts alongside its own configuration.',
  type: 'object',
  properties: {
    staleWarnSec: {
      type: 'integer',
      minimum: 1,
      title: 'Stale warning (s)',
      description: 'Health turns to warn when no report has arrived for this long.',
    },
    staleCriticalSec: {
      type: 'integer',
      minimum: 1,
      title: 'Stale critical (s)',
      description: 'Health turns to critical when no report has arrived for this long.',
    },
    backpressure: {
      type: 'string',
      enum: ['block', 'drop', 'nack'],
      title: 'Backpressure policy',
    },
    pinnedReplica: {
      type: 'string',
      title: 'Pinned replica',
      description: 'Run this feed only on the named ingest replica (required for listening transports).',
    },
    rawPayload: {
      type: 'string',
      enum: ['none', 'bounded', 'full'],
      default: 'bounded',
      title: 'Raw payload retention',
    },
    outbound: {
      type: 'object',
      title: 'Delivery tuning',
      description: 'How a sending connection paces what it delivers. Only read for connections that send.',
      properties: {
        queueMax: { type: 'integer', minimum: 1, title: 'Queue max', description: 'Events waiting to go out before the oldest are dropped.' },
        perEntityMinSec: { type: 'number', minimum: 0, title: 'Per-entity minimum (s)', description: 'Updates for one entity closer together than this are coalesced.' },
        maxEventsPerSec: { type: 'number', minimum: 0, title: 'Max events per second' },
        maxLagSec: { type: 'number', minimum: 0, title: 'Max lag (s)', description: 'Events older than this are dropped rather than delivered late.' },
        policyExitGraceSec: { type: 'number', minimum: 0, title: 'Policy exit grace (s)', description: 'How long an entity that left the policy keeps being delivered.' },
      },
    },
  },
};

/** The keys of the common options, for telling them apart from a driver's own. */
export const COMMON_OPTION_KEYS: readonly string[] = Object.keys(COMMON_OPTIONS_SCHEMA.properties ?? {});
