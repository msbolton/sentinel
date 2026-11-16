import { HttpClient, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { SearchRequest, SearchResponse } from '../../shared/models/search.model';

/** Full-text search over the search service's entity index. */
@Injectable({ providedIn: 'root' })
export class SearchService {
  private readonly http = inject(HttpClient);
  private readonly url = '/api/v1/search';

  search(req: SearchRequest): Observable<SearchResponse> {
    let params = new HttpParams().set('q', req.q);
    if (req.types?.length) params = params.set('types', req.types.join(','));
    if (req.sources?.length) params = params.set('sources', req.sources.join(','));
    if (req.classifications?.length) params = params.set('classifications', req.classifications.join(','));
    if (req.bounds) {
      params = params
        .set('north', String(req.bounds.north))
        .set('south', String(req.bounds.south))
        .set('east', String(req.bounds.east))
        .set('west', String(req.bounds.west));
    }
    if (req.page) params = params.set('page', String(req.page));
    if (req.pageSize) params = params.set('pageSize', String(req.pageSize));
    return this.http.get<SearchResponse>(this.url, { params });
  }
}
