import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';
import { CreateIngestTokenRequest, CreatedIngestToken, IngestToken } from '../../shared/models/ingest-token.model';

/** A gRPC connection's ingest tokens. Holds nothing: the Tokens section keeps what it was answered. */
@Injectable({ providedIn: 'root' })
export class IngestTokenService {
  private readonly http = inject(HttpClient);

  private url(feedId: string): string {
    return `/api/v1/feeds/${encodeURIComponent(feedId)}/tokens`;
  }

  list(feedId: string): Observable<IngestToken[]> {
    return this.http.get<IngestToken[]>(this.url(feedId));
  }

  create(feedId: string, body: CreateIngestTokenRequest): Observable<CreatedIngestToken> {
    return this.http.post<CreatedIngestToken>(this.url(feedId), body);
  }

  revoke(feedId: string, id: string): Observable<void> {
    return this.http.delete<void>(`${this.url(feedId)}/${encodeURIComponent(id)}`);
  }
}
