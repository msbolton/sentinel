package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// ErrNotFound is returned when a link, or an entity a link needs, is not
// there or not visible to the caller.
var ErrNotFound = errors.New("not found")

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

// Filter narrows the links a query returns.
type Filter struct {
	LinkTypes     []string
	MinConfidence *float64
	// Levels are the classification levels the caller may see. A link is
	// visible only when both of its entities are: a link to an entity above
	// the caller's clearance would reveal that the entity exists.
	Levels []string
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

const linkColumns = `l.id, l."sourceEntityId", l."targetEntityId", l."linkType", l.confidence, l.description, l.evidence,
	l."firstObserved"::text, l."lastObserved"::text, l.metadata, l."createdAt"::text`

// visibleLinks is the FROM clause that keeps only links between entities the
// caller may see; $levels is the placeholder holding Filter.Levels.
func visibleLinks(levels string) string {
	return fmt.Sprintf(`sentinel.links l
		JOIN sentinel.entities s ON s.id = l."sourceEntityId" AND NOT s.deleted AND s.classification = ANY(%[1]s::text[])
		JOIN sentinel.entities t ON t.id = l."targetEntityId" AND NOT t.deleted AND t.classification = ANY(%[1]s::text[])`, levels)
}

// Create inserts a new link and returns the created record. Both entities
// must exist and be visible to the caller, else ErrNotFound.
func (s *LinkStore) Create(ctx context.Context, p CreateLinkParams, levels []string) (*LinkRecord, error) {
	evidence := p.Evidence
	if evidence == nil {
		evidence = []string{}
	}
	metadata := p.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	var visible int
	err := s.pool.QueryRow(ctx, `
		SELECT count(DISTINCT id) FROM sentinel.entities
		WHERE id = ANY($1::uuid[]) AND NOT deleted AND classification = ANY($2::text[])
	`, []string{p.SourceEntityID, p.TargetEntityID}, levels).Scan(&visible)
	if err != nil {
		return nil, fmt.Errorf("check link entities: %w", err)
	}
	if visible < 2 {
		return nil, ErrNotFound
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.links AS l
			("sourceEntityId", "targetEntityId", "linkType", confidence, description, evidence, "firstObserved", "lastObserved", metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+linkColumns,
		p.SourceEntityID, p.TargetEntityID, p.LinkType, p.Confidence, p.Description, evidence, p.FirstObserved, p.LastObserved, metadata)

	rec, err := scanLink(row)
	if err != nil {
		return nil, fmt.Errorf("create link: %w", err)
	}
	return rec, nil
}

// Delete removes a link the caller can see; ErrNotFound otherwise.
func (s *LinkStore) Delete(ctx context.Context, id string, levels []string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM sentinel.links WHERE id = $1 AND id IN (SELECT l.id FROM `+visibleLinks("$2")+`)
	`, id, levels)
	if err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetLinks returns the visible links of an entity, newest first.
func (s *LinkStore) GetLinks(ctx context.Context, entityID string, f Filter) ([]LinkRecord, error) {
	args := []interface{}{f.Levels, entityID}
	where := []string{`(l."sourceEntityId" = $2 OR l."targetEntityId" = $2)`}
	where, args = appendFilter(where, args, f)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM %s WHERE %s ORDER BY l."createdAt" DESC
	`, linkColumns, visibleLinks("$1"), strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, fmt.Errorf("get links: %w", err)
	}
	return scanLinks(rows)
}

// appendFilter adds the type and confidence conditions to a WHERE list.
func appendFilter(where []string, args []interface{}, f Filter) ([]string, []interface{}) {
	if len(f.LinkTypes) > 0 {
		args = append(args, f.LinkTypes)
		where = append(where, fmt.Sprintf(`l."linkType" = ANY($%d::text[])`, len(args)))
	}
	if f.MinConfidence != nil {
		args = append(args, *f.MinConfidence)
		where = append(where, fmt.Sprintf(`l.confidence >= $%d`, len(args)))
	}
	return where, args
}

// GetGraph traverses visible links from a center entity up to maxDepth
// using BFS. The center is a node even when it has no links; a center the
// caller may not see yields an empty graph.
func (s *LinkStore) GetGraph(ctx context.Context, centerID string, maxDepth int, f Filter) (*GraphResult, error) {
	edges := []GraphEdge{}
	visited := map[string]bool{centerID: true}
	frontier := []string{centerID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		args := []interface{}{f.Levels, frontier}
		where := []string{`(l."sourceEntityId" = ANY($2::uuid[]) OR l."targetEntityId" = ANY($2::uuid[]))`}
		where, args = appendFilter(where, args, f)

		rows, err := s.pool.Query(ctx, fmt.Sprintf(`
			SELECT %s FROM %s WHERE %s
		`, linkColumns, visibleLinks("$1"), strings.Join(where, " AND ")), args...)
		if err != nil {
			return nil, fmt.Errorf("get graph depth %d: %w", depth, err)
		}
		links, err := scanLinks(rows)
		if err != nil {
			return nil, err
		}

		seenEdge := map[string]bool{}
		for _, e := range edges {
			seenEdge[e.ID] = true
		}
		nextFrontier := []string{}
		for _, link := range links {
			if seenEdge[link.ID] {
				continue
			}
			seenEdge[link.ID] = true
			edges = append(edges, edgeOf(link))
			for _, eid := range []string{link.SourceEntityID, link.TargetEntityID} {
				if !visited[eid] {
					visited[eid] = true
					nextFrontier = append(nextFrontier, eid)
				}
			}
		}
		frontier = nextFrontier
	}

	ids := make([]string, 0, len(visited))
	for id := range visited {
		ids = append(ids, id)
	}
	nodes, err := s.nodesOf(ctx, ids, f.Levels)
	if err != nil {
		return nil, err
	}
	return &GraphResult{Nodes: nodes, Edges: edges}, nil
}

// FindShortestPath finds the shortest path between two entities over
// visible links using BFS.
func (s *LinkStore) FindShortestPath(ctx context.Context, fromID, toID string, levels []string) (*GraphResult, error) {
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
			edges := make([]GraphEdge, len(current.links))
			for i, link := range current.links {
				edges[i] = edgeOf(link)
			}
			nodes, err := s.nodesOf(ctx, current.path, levels)
			if err != nil {
				return nil, err
			}
			return &GraphResult{Nodes: nodes, Edges: edges}, nil
		}

		links, err := s.GetLinks(ctx, current.entityID, Filter{Levels: levels})
		if err != nil {
			return nil, err
		}

		for _, link := range links {
			neighbor := link.TargetEntityID
			if neighbor == current.entityID {
				neighbor = link.SourceEntityID
			}
			if visited[neighbor] {
				continue
			}
			visited[neighbor] = true
			queue = append(queue, queueItem{
				entityID: neighbor,
				path:     append(append([]string{}, current.path...), neighbor),
				links:    append(append([]LinkRecord{}, current.links...), link),
			})
		}
	}

	return &GraphResult{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
}

// nodesOf fetches the visible entities among ids as graph nodes, in the
// order of ids.
func (s *LinkStore) nodesOf(ctx context.Context, ids []string, levels []string) ([]GraphNode, error) {
	nodes := []GraphNode{}
	if len(ids) == 0 {
		return nodes, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, "entityType", name, classification,
			ST_Y(position::geometry) AS lat, ST_X(position::geometry) AS lng
		FROM sentinel.entities
		WHERE id = ANY($1::uuid[]) AND NOT deleted AND classification = ANY($2::text[])
	`, ids, levels)
	if err != nil {
		return nil, fmt.Errorf("get graph nodes: %w", err)
	}
	defer rows.Close()

	byID := map[string]GraphNode{}
	for rows.Next() {
		var node GraphNode
		var lat, lng *float64
		if err := rows.Scan(&node.EntityID, &node.EntityType, &node.Name, &node.Classification, &lat, &lng); err != nil {
			return nil, fmt.Errorf("scan graph node: %w", err)
		}
		if lat != nil && lng != nil {
			node.Position = &Position{Lat: *lat, Lon: *lng}
		}
		byID[node.EntityID] = node
	}
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

// Ping checks database connectivity.
func (s *LinkStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func edgeOf(link LinkRecord) GraphEdge {
	return GraphEdge{
		ID:             link.ID,
		SourceEntityID: link.SourceEntityID,
		TargetEntityID: link.TargetEntityID,
		LinkType:       link.LinkType,
		Confidence:     link.Confidence,
	}
}

func scanLink(row pgx.Row) (*LinkRecord, error) {
	var rec LinkRecord
	err := row.Scan(
		&rec.ID, &rec.SourceEntityID, &rec.TargetEntityID, &rec.LinkType,
		&rec.Confidence, &rec.Description, &rec.Evidence,
		&rec.FirstObserved, &rec.LastObserved, &rec.Metadata, &rec.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func scanLinks(rows pgx.Rows) ([]LinkRecord, error) {
	defer rows.Close()
	links := []LinkRecord{}
	for rows.Next() {
		rec, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, *rec)
	}
	return links, rows.Err()
}

// GraphNode represents a node in the entity graph.
type GraphNode struct {
	EntityID       string    `json:"entityId"`
	EntityType     string    `json:"entityType"`
	Name           string    `json:"name"`
	Classification string    `json:"classification"`
	Position       *Position `json:"position,omitempty"`
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
