package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// LinkRecord represents a link row from the database.
type LinkRecord struct {
	ID             string            `json:"id"`
	SourceEntityID string            `json:"sourceEntityId"`
	TargetEntityID string            `json:"targetEntityId"`
	LinkType       string            `json:"linkType"`
	Confidence     float64           `json:"confidence"`
	Description    *string           `json:"description,omitempty"`
	Evidence       []string          `json:"evidence"`
	FirstObserved  *string           `json:"firstObserved,omitempty"`
	LastObserved   *string           `json:"lastObserved,omitempty"`
	Metadata       map[string]string `json:"metadata"`
	CreatedAt      string            `json:"createdAt"`
}

// CreateLinkParams holds parameters for creating a link.
type CreateLinkParams struct {
	SourceEntityID string
	TargetEntityID string
	LinkType       string
	Confidence     float64
	Description    *string
	Evidence       []string
	FirstObserved  *string
	LastObserved   *string
	Metadata       map[string]string
}

// LinkStore handles database operations for links.
type LinkStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewLinkStore creates a new link store.
func NewLinkStore(pool *pgxpool.Pool, logger *zap.Logger) *LinkStore {
	return &LinkStore{pool: pool, logger: logger}
}

// Create inserts a new link and returns the created record.
func (s *LinkStore) Create(ctx context.Context, p CreateLinkParams) (*LinkRecord, error) {
	evidence := p.Evidence
	if evidence == nil {
		evidence = []string{}
	}
	metadata := p.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.links
			("sourceEntityId", "targetEntityId", "linkType", confidence, description, evidence, "firstObserved", "lastObserved", metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, "sourceEntityId", "targetEntityId", "linkType", confidence, description, evidence,
			"firstObserved"::text, "lastObserved"::text, metadata, "createdAt"::text
	`, p.SourceEntityID, p.TargetEntityID, p.LinkType, p.Confidence, p.Description, evidence, p.FirstObserved, p.LastObserved, metadata)

	var rec LinkRecord
	err := row.Scan(
		&rec.ID, &rec.SourceEntityID, &rec.TargetEntityID, &rec.LinkType,
		&rec.Confidence, &rec.Description, &rec.Evidence,
		&rec.FirstObserved, &rec.LastObserved, &rec.Metadata, &rec.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create link: %w", err)
	}

	return &rec, nil
}

// Delete removes a link by ID.
func (s *LinkStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sentinel.links WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("link not found: %s", id)
	}
	return nil
}

// GetLinks returns links for an entity with optional filters.
func (s *LinkStore) GetLinks(ctx context.Context, entityID string, linkTypes []string, minConfidence *float64) ([]LinkRecord, error) {
	where := []string{`("sourceEntityId" = $1 OR "targetEntityId" = $1)`}
	args := []interface{}{entityID}
	argIdx := 2

	if len(linkTypes) > 0 {
		placeholders := make([]string, len(linkTypes))
		for i, lt := range linkTypes {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, lt)
			argIdx++
		}
		where = append(where, fmt.Sprintf(`"linkType" IN (%s)`, strings.Join(placeholders, ",")))
	}

	if minConfidence != nil {
		where = append(where, fmt.Sprintf(`confidence >= $%d`, argIdx))
		args = append(args, *minConfidence)
	}

	query := fmt.Sprintf(`
		SELECT id, "sourceEntityId", "targetEntityId", "linkType", confidence, description, evidence,
			"firstObserved"::text, "lastObserved"::text, metadata, "createdAt"::text
		FROM sentinel.links
		WHERE %s
		ORDER BY "createdAt" DESC
	`, strings.Join(where, " AND "))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get links: %w", err)
	}
	defer rows.Close()

	return scanLinks(rows)
}

// GetGraph traverses links from a center entity up to maxDepth using BFS.
func (s *LinkStore) GetGraph(ctx context.Context, centerID string, maxDepth int, linkTypes []string, minConfidence *float64) (*GraphResult, error) {
	nodes := map[string]*GraphNode{}
	edges := []GraphEdge{}
	visited := map[string]bool{centerID: true}
	frontier := []string{centerID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		// Build query for all frontier entities.
		placeholders := make([]string, len(frontier))
		args := make([]interface{}, len(frontier))
		for i, id := range frontier {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}
		argIdx := len(frontier) + 1

		where := []string{fmt.Sprintf(`("sourceEntityId" IN (%s) OR "targetEntityId" IN (%s))`,
			strings.Join(placeholders, ","), strings.Join(placeholders, ","))}

		if len(linkTypes) > 0 {
			ltPlaceholders := make([]string, len(linkTypes))
			for i, lt := range linkTypes {
				ltPlaceholders[i] = fmt.Sprintf("$%d", argIdx)
				args = append(args, lt)
				argIdx++
			}
			where = append(where, fmt.Sprintf(`"linkType" IN (%s)`, strings.Join(ltPlaceholders, ",")))
		}

		if minConfidence != nil {
			where = append(where, fmt.Sprintf(`confidence >= $%d`, argIdx))
			args = append(args, *minConfidence)
		}

		query := fmt.Sprintf(`
			SELECT id, "sourceEntityId", "targetEntityId", "linkType", confidence, description, evidence,
				"firstObserved"::text, "lastObserved"::text, metadata, "createdAt"::text
			FROM sentinel.links
			WHERE %s
		`, strings.Join(where, " AND "))

		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("get graph depth %d: %w", depth, err)
		}

		links, err := scanLinks(rows)
		if err != nil {
			return nil, err
		}

		nextFrontier := []string{}
		for _, link := range links {
			edges = append(edges, GraphEdge{
				ID:             link.ID,
				SourceEntityID: link.SourceEntityID,
				TargetEntityID: link.TargetEntityID,
				LinkType:       link.LinkType,
				Confidence:     link.Confidence,
			})

			for _, eid := range []string{link.SourceEntityID, link.TargetEntityID} {
				if !visited[eid] {
					visited[eid] = true
					nextFrontier = append(nextFrontier, eid)
				}
			}
		}

		frontier = nextFrontier
	}

	// Fetch entity info for all visited nodes.
	if len(visited) > 0 {
		entityIDs := make([]string, 0, len(visited))
		for id := range visited {
			entityIDs = append(entityIDs, id)
		}

		placeholders := make([]string, len(entityIDs))
		args := make([]interface{}, len(entityIDs))
		for i, id := range entityIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}

		query := fmt.Sprintf(`
			SELECT id, "entityType", name,
				ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng
			FROM sentinel.entities
			WHERE id IN (%s) AND deleted = false
		`, strings.Join(placeholders, ","))

		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			s.logger.Warn("failed to fetch entity info for graph nodes", zap.Error(err))
		} else {
			defer rows.Close()
			for rows.Next() {
				var node GraphNode
				var lat, lng *float64
				if err := rows.Scan(&node.EntityID, &node.EntityType, &node.Name, &lat, &lng); err != nil {
					continue
				}
				if lat != nil && lng != nil {
					node.Position = &Position{Lat: *lat, Lon: *lng}
				}
				nodes[node.EntityID] = &node
			}
		}
	}

	nodeList := make([]GraphNode, 0, len(nodes))
	for _, n := range nodes {
		nodeList = append(nodeList, *n)
	}

	return &GraphResult{Nodes: nodeList, Edges: edges}, nil
}

// FindShortestPath finds the shortest path between two entities using BFS.
func (s *LinkStore) FindShortestPath(ctx context.Context, fromID, toID string) (*GraphResult, error) {
	type queueItem struct {
		entityID string
		path     []string
		links    []LinkRecord
	}

	visited := map[string]bool{fromID: true}
	queue := []queueItem{{entityID: fromID, path: []string{fromID}, links: nil}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.entityID == toID {
			return s.buildPathResult(ctx, current.path, current.links)
		}

		links, err := s.GetLinks(ctx, current.entityID, nil, nil)
		if err != nil {
			return nil, err
		}

		for _, link := range links {
			neighbor := link.TargetEntityID
			if neighbor == current.entityID {
				neighbor = link.SourceEntityID
			}

			if !visited[neighbor] {
				visited[neighbor] = true
				newPath := make([]string, len(current.path)+1)
				copy(newPath, current.path)
				newPath[len(current.path)] = neighbor

				newLinks := make([]LinkRecord, len(current.links)+1)
				copy(newLinks, current.links)
				newLinks[len(current.links)] = link

				queue = append(queue, queueItem{
					entityID: neighbor,
					path:     newPath,
					links:    newLinks,
				})
			}
		}
	}

	return &GraphResult{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
}

func (s *LinkStore) buildPathResult(ctx context.Context, path []string, links []LinkRecord) (*GraphResult, error) {
	edges := make([]GraphEdge, len(links))
	for i, link := range links {
		edges[i] = GraphEdge{
			ID:             link.ID,
			SourceEntityID: link.SourceEntityID,
			TargetEntityID: link.TargetEntityID,
			LinkType:       link.LinkType,
			Confidence:     link.Confidence,
		}
	}

	// Fetch entity info for path nodes.
	nodes := []GraphNode{}
	if len(path) > 0 {
		placeholders := make([]string, len(path))
		args := make([]interface{}, len(path))
		for i, id := range path {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}

		query := fmt.Sprintf(`
			SELECT id, "entityType", name,
				ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng
			FROM sentinel.entities
			WHERE id IN (%s) AND deleted = false
		`, strings.Join(placeholders, ","))

		rows, err := s.pool.Query(ctx, query, args...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var node GraphNode
				var lat, lng *float64
				if err := rows.Scan(&node.EntityID, &node.EntityType, &node.Name, &lat, &lng); err != nil {
					continue
				}
				if lat != nil && lng != nil {
					node.Position = &Position{Lat: *lat, Lon: *lng}
				}
				nodes = append(nodes, node)
			}
		}
	}

	return &GraphResult{Nodes: nodes, Edges: edges}, nil
}

// Ping checks database connectivity.
func (s *LinkStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func scanLinks(rows pgx.Rows) ([]LinkRecord, error) {
	defer rows.Close()
	var links []LinkRecord
	for rows.Next() {
		var rec LinkRecord
		err := rows.Scan(
			&rec.ID, &rec.SourceEntityID, &rec.TargetEntityID, &rec.LinkType,
			&rec.Confidence, &rec.Description, &rec.Evidence,
			&rec.FirstObserved, &rec.LastObserved, &rec.Metadata, &rec.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, rec)
	}
	return links, nil
}

// GraphNode represents a node in the entity graph.
type GraphNode struct {
	EntityID   string    `json:"entityId"`
	EntityType string    `json:"entityType"`
	Name       string    `json:"name"`
	Position   *Position `json:"position,omitempty"`
}

// Position represents a geographic position.
type Position struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// GraphEdge represents an edge in the entity graph.
type GraphEdge struct {
	ID             string  `json:"id"`
	SourceEntityID string  `json:"sourceEntityId"`
	TargetEntityID string  `json:"targetEntityId"`
	LinkType       string  `json:"linkType"`
	Confidence     float64 `json:"confidence"`
}

// GraphResult represents a graph traversal result.
type GraphResult struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}
