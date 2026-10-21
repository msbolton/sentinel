/** An ingest token as its connection's operators see it: never the secret. */
export interface IngestToken {
  id: string;
  feedId: string;
  name: string;
  /** "si_" plus eight characters, to tell tokens apart. */
  prefix: string;
  /** The ceiling: no report above it is accepted. */
  classification: string;
  createdBy: string;
  createdAt: string;
  lastUsedAt: string | null;
  expiresAt: string | null;
  revokedAt: string | null;
}

/** The create answer: the only time the secret is shown. */
export interface CreatedIngestToken extends IngestToken {
  token: string;
}

export interface CreateIngestTokenRequest {
  name: string;
  classification?: string;
  expiresAt?: string;
}
