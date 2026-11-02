import { HttpClient, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { CreateLinkRequest, GraphRequest, GraphResult, Link } from '../../shared/models/link.model';

/** Links between entities and the graph the link-analysis service walks over them. */
@Injectable({ providedIn: 'root' })
export class LinkService {
  private readonly http = inject(HttpClient);
  private readonly url = '/api/v1/links';

  /** The graph around one entity, with the server's parameter names. */
  graph(req: GraphRequest): Observable<GraphResult> {
    let params = new HttpParams().set('centerId', req.centerId);
    if (req.maxDepth) params = params.set('maxDepth', String(req.maxDepth));
    if (req.types?.length) params = params.set('types', req.types.join(','));
    if (req.minConfidence !== undefined) params = params.set('minConfidence', String(req.minConfidence));
    return this.http.get<GraphResult>(`${this.url}/graph`, { params });
  }

  /** Every link touching an entity, newest first. */
  links(entityId: string): Observable<Link[]> {
    return this.http.get<Link[]>(this.url, { params: new HttpParams().set('entityId', entityId) });
  }

  create(req: CreateLinkRequest): Observable<Link> {
    return this.http.post<Link>(this.url, req);
  }

  delete(id: string): Observable<void> {
    return this.http.delete<void>(`${this.url}/${id}`);
  }
}
