import { Injectable, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, tap } from 'rxjs';

/** A key as `GET /api/v1/auth/api-keys` lists it: the secret is never returned again. */
export interface ApiKey {
  id: string;
  name: string;
  /** The first characters of the secret, enough to recognise it in a tool's config. */
  prefix: string;
  classification: string;
  createdAt: string;
  expiresAt?: string;
  lastUsedAt?: string;
}

/** `POST /api/v1/auth/api-keys` answers with the key and, this once, its secret. */
export interface CreatedApiKey extends ApiKey {
  secret: string;
}

export interface CreateApiKeyRequest {
  name: string;
  classification?: string;
  expiresInDays?: number;
}

/**
 * Personal API keys for GIS tools and KML network links (`?api_key=` on
 * `/api/v1/export/...`). Keys are read-only and capped at the issuer's
 * clearance. Older gateways do not serve the endpoint; `available`
 * turns false on a 404 so the UI can hide itself.
 */
@Injectable({ providedIn: 'root' })
export class ApiKeyService {
  private readonly http = inject(HttpClient);
  private readonly url = '/api/v1/auth/api-keys';

  private readonly keysState = signal<ApiKey[]>([]);

  readonly keys = this.keysState.asReadonly();
  /** null until the first list answers; false when the gateway has no API keys. */
  readonly available = signal<boolean | null>(null);

  list(): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(this.url).pipe(
      tap({
        next: (keys) => {
          this.available.set(true);
          this.keysState.set(keys ?? []);
        },
        error: (err: unknown) => {
          if (err instanceof HttpErrorResponse && err.status === 404) {
            this.available.set(false);
          }
        },
      }),
    );
  }

  create(body: CreateApiKeyRequest): Observable<CreatedApiKey> {
    return this.http.post<CreatedApiKey>(this.url, body).pipe(
      tap((created) => {
        const { secret: _secret, ...key } = created;
        this.keysState.update((keys) => [...keys, key]);
      }),
    );
  }

  revoke(id: string): Observable<void> {
    return this.http
      .delete<void>(`${this.url}/${encodeURIComponent(id)}`)
      .pipe(tap(() => this.keysState.update((keys) => keys.filter((k) => k.id !== id))));
  }
}
