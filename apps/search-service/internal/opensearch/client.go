package opensearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	opensearchapi "github.com/opensearch-project/opensearch-go/v2"
	"go.uber.org/zap"
)

const indexName = "sentinel-entities"

// Client wraps the OpenSearch client with search-service specific operations.
type Client struct {
	os     *opensearchapi.Client
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewClient creates a new OpenSearch client wrapper.
func NewClient(host string, pool *pgxpool.Pool, logger *zap.Logger) (*Client, error) {
	cfg := opensearchapi.Config{
		Addresses: []string{host},
	}

	if strings.HasPrefix(host, "https://") {
		cfg.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	client, err := opensearchapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("opensearch client: %w", err)
	}

	return &Client{
		os:     client,
		pool:   pool,
		logger: logger,
	}, nil
}

// EnsureIndex creates the sentinel-entities index if it doesn't exist.
func (c *Client) EnsureIndex(ctx context.Context) error {
	res, err := c.os.Indices.Exists([]string{indexName})
	if err != nil {
		return fmt.Errorf("check index existence: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == 200 {
		c.logger.Info("opensearch index already exists", zap.String("index", indexName))
		return nil
	}

	settings := map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   3,
			"number_of_replicas": 1,
			"analysis": map[string]interface{}{
				"analyzer": map[string]interface{}{
					"autocomplete_analyzer": map[string]interface{}{
						"type":      "custom",
						"tokenizer": "autocomplete_tokenizer",
						"filter":    []string{"lowercase"},
					},
				},
				"tokenizer": map[string]interface{}{
					"autocomplete_tokenizer": map[string]interface{}{
						"type":        "edge_ngram",
						"min_gram":    2,
						"max_gram":    20,
						"token_chars": []string{"letter", "digit"},
					},
				},
			},
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type": "text",
					"fields": map[string]interface{}{
						"keyword":      map[string]interface{}{"type": "keyword"},
						"autocomplete": map[string]interface{}{"type": "text", "analyzer": "autocomplete_analyzer"},
					},
				},
				"description":     map[string]interface{}{"type": "text"},
				"entityType":      map[string]interface{}{"type": "keyword"},
				"source":          map[string]interface{}{"type": "keyword"},
				"classification":  map[string]interface{}{"type": "keyword"},
				"position":        map[string]interface{}{"type": "geo_point"},
				"affiliations":    map[string]interface{}{"type": "keyword"},
				"metadata":        map[string]interface{}{"type": "object"},
				"createdAt":       map[string]interface{}{"type": "date"},
				"updatedAt":       map[string]interface{}{"type": "date"},
				"lastSeenAt":      map[string]interface{}{"type": "date"},
				"affiliation":     map[string]interface{}{"type": "keyword"},
				"trackEnvironment": map[string]interface{}{"type": "keyword"},
				"operationalStatus": map[string]interface{}{"type": "keyword"},
				"countryOfOrigin":   map[string]interface{}{"type": "keyword"},
				"sourceEntityId":    map[string]interface{}{"type": "keyword"},
				"platformIdentifiers": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"mmsi":         map[string]interface{}{"type": "keyword"},
						"imo":          map[string]interface{}{"type": "keyword"},
						"icaoHex":      map[string]interface{}{"type": "keyword"},
						"registration": map[string]interface{}{"type": "keyword"},
						"callsign":     map[string]interface{}{"type": "keyword"},
						"noradId":      map[string]interface{}{"type": "integer"},
						"squawk":       map[string]interface{}{"type": "keyword"},
					},
				},
			},
		},
	}

	body, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal index settings: %w", err)
	}

	createRes, err := c.os.Indices.Create(indexName, c.os.Indices.Create.WithBody(bytes.NewReader(body)))
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer createRes.Body.Close()

	if createRes.IsError() {
		respBody, _ := io.ReadAll(createRes.Body)
		return fmt.Errorf("create index failed: %s", string(respBody))
	}

	c.logger.Info("opensearch index created", zap.String("index", indexName))
	return nil
}

// WarmIndex seeds the index from PostgreSQL if it's empty.
func (c *Client) WarmIndex(ctx context.Context) error {
	// Check current document count.
	countBody := `{"query":{"match_all":{}}}`
	countRes, err := c.os.Count(
		c.os.Count.WithContext(ctx),
		c.os.Count.WithIndex(indexName),
		c.os.Count.WithBody(strings.NewReader(countBody)),
	)
	if err != nil {
		c.logger.Warn("failed to check index count", zap.Error(err))
		return nil
	}
	defer countRes.Body.Close()

	var countResult struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(countRes.Body).Decode(&countResult); err != nil {
		c.logger.Warn("failed to decode count response", zap.Error(err))
		return nil
	}

	if countResult.Count > 0 {
		c.logger.Info("index already populated, skipping warm", zap.Int("count", countResult.Count))
		return nil
	}

	// Query all entities from PostgreSQL.
	rows, err := c.pool.Query(ctx, `
		SELECT id, "entityType", name, description, source, classification,
			ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng,
			affiliations, metadata, "createdAt", "updatedAt", "lastSeenAt"
		FROM sentinel.entities
		WHERE deleted = false
	`)
	if err != nil {
		c.logger.Warn("failed to query entities for index warming", zap.Error(err))
		return nil
	}
	defer rows.Close()

	var bulkBuf bytes.Buffer
	count := 0

	for rows.Next() {
		var doc EntityDocument
		var lat, lng *float64
		var affiliations []string
		var metadata map[string]interface{}

		err := rows.Scan(
			&doc.ID, &doc.EntityType, &doc.Name, &doc.Description,
			&doc.Source, &doc.Classification,
			&lat, &lng,
			&affiliations, &metadata,
			&doc.CreatedAt, &doc.UpdatedAt, &doc.LastSeenAt,
		)
		if err != nil {
			c.logger.Warn("failed to scan entity row", zap.Error(err))
			continue
		}

		if lat != nil && lng != nil {
			doc.Position = &GeoPoint{Lat: *lat, Lon: *lng}
		}
		doc.Affiliations = affiliations
		doc.Metadata = metadata

		// Write bulk action line.
		action := map[string]interface{}{
			"index": map[string]interface{}{
				"_index": indexName,
				"_id":    doc.ID,
			},
		}
		actionBytes, _ := json.Marshal(action)
		bulkBuf.Write(actionBytes)
		bulkBuf.WriteByte('\n')

		docBytes, _ := json.Marshal(doc)
		bulkBuf.Write(docBytes)
		bulkBuf.WriteByte('\n')
		count++
	}

	if count == 0 {
		c.logger.Info("no entities to warm index with")
		return nil
	}

	bulkRes, err := c.os.Bulk(
		bytes.NewReader(bulkBuf.Bytes()),
		c.os.Bulk.WithContext(ctx),
		c.os.Bulk.WithRefresh("wait_for"),
	)
	if err != nil {
		c.logger.Warn("bulk index failed", zap.Error(err))
		return nil
	}
	defer bulkRes.Body.Close()

	if bulkRes.IsError() {
		respBody, _ := io.ReadAll(bulkRes.Body)
		c.logger.Warn("bulk index error response", zap.String("body", string(respBody)))
		return nil
	}

	c.logger.Info("index warmed from database", zap.Int("documents", count))
	return nil
}

// IndexEntity indexes or updates an entity document.
func (c *Client) IndexEntity(ctx context.Context, doc EntityDocument) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal entity document: %w", err)
	}

	res, err := c.os.Index(
		indexName,
		bytes.NewReader(body),
		c.os.Index.WithContext(ctx),
		c.os.Index.WithDocumentID(doc.ID),
		c.os.Index.WithRefresh("wait_for"),
	)
	if err != nil {
		return fmt.Errorf("index entity: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("index entity failed: %s", string(respBody))
	}

	return nil
}

// DeleteEntity removes an entity from the index.
func (c *Client) DeleteEntity(ctx context.Context, id string) {
	res, err := c.os.Delete(
		indexName,
		id,
		c.os.Delete.WithContext(ctx),
		c.os.Delete.WithRefresh("wait_for"),
	)
	if err != nil {
		c.logger.Warn("failed to delete entity from index", zap.String("id", id), zap.Error(err))
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		c.logger.Warn("delete entity from index failed", zap.String("id", id), zap.Int("status", res.StatusCode))
	}
}

// Search performs a full-text search with optional geo-bounding box and facets.
func (c *Client) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	must := []interface{}{}
	filter := []interface{}{}

	if q.Q != "" {
		must = append(must, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":     q.Q,
				"fields":    []string{"name^3", "name.keyword^5", "description", "affiliations"},
				"type":      "best_fields",
				"fuzziness": "AUTO",
			},
		})
	}

	if q.North != nil && q.South != nil && q.East != nil && q.West != nil {
		filter = append(filter, map[string]interface{}{
			"geo_bounding_box": map[string]interface{}{
				"position": map[string]interface{}{
					"top_left": map[string]interface{}{
						"lat": *q.North,
						"lon": *q.West,
					},
					"bottom_right": map[string]interface{}{
						"lat": *q.South,
						"lon": *q.East,
					},
				},
			},
		})
	}

	if len(q.Types) > 0 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"entityType": q.Types},
		})
	}

	if len(q.Sources) > 0 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"source": q.Sources},
		})
	}

	if len(q.Classifications) > 0 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"classification": q.Classifications},
		})
	}

	if len(must) == 0 {
		must = append(must, map[string]interface{}{"match_all": map[string]interface{}{}})
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   must,
				"filter": filter,
			},
		},
		"from": (page - 1) * pageSize,
		"size": pageSize,
		"aggs": map[string]interface{}{
			"entityTypes": map[string]interface{}{
				"terms": map[string]interface{}{"field": "entityType", "size": 50},
			},
			"sources": map[string]interface{}{
				"terms": map[string]interface{}{"field": "source", "size": 50},
			},
			"classifications": map[string]interface{}{
				"terms": map[string]interface{}{"field": "classification", "size": 50},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, err := c.os.Search(
		c.os.Search.WithContext(ctx),
		c.os.Search.WithIndex(indexName),
		c.os.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("search failed: %s", string(respBody))
	}

	return c.parseSearchResponse(res.Body, page, pageSize, true)
}

// SearchNearby performs a geo-distance search with optional text query.
func (c *Client) SearchNearby(ctx context.Context, q NearbyQuery) (*SearchResult, error) {
	must := []interface{}{}
	filter := []interface{}{}

	if q.Q != "" {
		must = append(must, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":     q.Q,
				"fields":    []string{"name^3", "description"},
				"fuzziness": "AUTO",
			},
		})
	}

	filter = append(filter, map[string]interface{}{
		"geo_distance": map[string]interface{}{
			"distance": fmt.Sprintf("%.1fkm", q.RadiusKm),
			"position": map[string]interface{}{
				"lat": q.Lat,
				"lon": q.Lng,
			},
		},
	})

	if len(must) == 0 {
		must = append(must, map[string]interface{}{"match_all": map[string]interface{}{}})
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   must,
				"filter": filter,
			},
		},
		"sort": []interface{}{
			map[string]interface{}{
				"_geo_distance": map[string]interface{}{
					"position": map[string]interface{}{
						"lat": q.Lat,
						"lon": q.Lng,
					},
					"order": "asc",
					"unit":  "km",
				},
			},
		},
		"from": (page - 1) * pageSize,
		"size": pageSize,
	}

	body, _ := json.Marshal(query)
	res, err := c.os.Search(
		c.os.Search.WithContext(ctx),
		c.os.Search.WithIndex(indexName),
		c.os.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("search nearby: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("search nearby failed: %s", string(respBody))
	}

	return c.parseSearchResponse(res.Body, page, pageSize, false)
}

// Suggest returns autocomplete suggestions for a prefix.
func (c *Client) Suggest(ctx context.Context, prefix string) ([]SuggestResult, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"match": map[string]interface{}{
				"name.autocomplete": map[string]interface{}{
					"query":    prefix,
					"operator": "and",
				},
			},
		},
		"size": 10,
		"_source": []string{"name", "entityType"},
	}

	body, _ := json.Marshal(query)
	res, err := c.os.Search(
		c.os.Search.WithContext(ctx),
		c.os.Search.WithIndex(indexName),
		c.os.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("suggest: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("suggest failed: %s", string(respBody))
	}

	var result osSearchResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode suggest response: %w", err)
	}

	suggestions := make([]SuggestResult, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var doc EntityDocument
		if err := json.Unmarshal(hit.Source, &doc); err != nil {
			continue
		}
		suggestions = append(suggestions, SuggestResult{
			ID:         hit.ID,
			Name:       doc.Name,
			EntityType: doc.EntityType,
		})
	}

	return suggestions, nil
}

// GetFacets returns only aggregation counts without documents.
func (c *Client) GetFacets(ctx context.Context) (*Facets, error) {
	query := map[string]interface{}{
		"size": 0,
		"aggs": map[string]interface{}{
			"entityTypes": map[string]interface{}{
				"terms": map[string]interface{}{"field": "entityType", "size": 100},
			},
			"sources": map[string]interface{}{
				"terms": map[string]interface{}{"field": "source", "size": 100},
			},
			"classifications": map[string]interface{}{
				"terms": map[string]interface{}{"field": "classification", "size": 100},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, err := c.os.Search(
		c.os.Search.WithContext(ctx),
		c.os.Search.WithIndex(indexName),
		c.os.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("get facets: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("get facets failed: %s", string(respBody))
	}

	var result osSearchResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode facets response: %w", err)
	}

	return parseFacets(result.Aggregations), nil
}

// Ping checks if OpenSearch is reachable.
func (c *Client) Ping(ctx context.Context) error {
	res, err := c.os.Ping(c.os.Ping.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("opensearch ping returned %d", res.StatusCode)
	}
	return nil
}

// parseSearchResponse parses the OpenSearch response into a SearchResult.
func (c *Client) parseSearchResponse(body io.Reader, page, pageSize int, includeFacets bool) (*SearchResult, error) {
	var result osSearchResponse
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	hits := make([]EntityDocument, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var doc EntityDocument
		if err := json.Unmarshal(hit.Source, &doc); err != nil {
			c.logger.Warn("failed to unmarshal hit", zap.Error(err))
			continue
		}
		doc.ID = hit.ID
		hits = append(hits, doc)
	}

	sr := &SearchResult{
		Total:    result.Hits.Total.Value,
		Page:     page,
		PageSize: pageSize,
		Hits:     hits,
	}

	if includeFacets && result.Aggregations != nil {
		sr.Facets = parseFacets(result.Aggregations)
	}

	return sr, nil
}

func parseFacets(aggs map[string]json.RawMessage) *Facets {
	if aggs == nil {
		return nil
	}

	facets := &Facets{
		EntityTypes:     map[string]int{},
		Sources:         map[string]int{},
		Classifications: map[string]int{},
	}

	parseBuckets := func(key string) map[string]int {
		result := map[string]int{}
		raw, ok := aggs[key]
		if !ok {
			return result
		}
		var agg struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(raw, &agg); err != nil {
			return result
		}
		for _, b := range agg.Buckets {
			result[b.Key] = b.DocCount
		}
		return result
	}

	facets.EntityTypes = parseBuckets("entityTypes")
	facets.Sources = parseBuckets("sources")
	facets.Classifications = parseBuckets("classifications")

	return facets
}
