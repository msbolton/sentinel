// Package export serves the live picture to lightweight GIS consumers as
// GeoJSON and KML. It reads entities through the entity service with the
// caller's identity, so the clearance ceiling the service applies holds
// here too.
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/proxy"
)

// Bounds is a bounding box in degrees.
type Bounds struct{ North, South, East, West float64 }

// Query selects entities.
type Query struct {
	BBox     *Bounds
	Types    []string
	Feeds    []string
	Page     int
	PageSize int
}

// Entity is the slice of the entity-service record the exports need.
type Entity struct {
	ID                string   `json:"id"`
	EntityType        string   `json:"entityType"`
	Name              string   `json:"name"`
	Description       *string  `json:"description"`
	Source            string   `json:"source"`
	Classification    string   `json:"classification"`
	FeedID            *string  `json:"feedId"`
	LastFeedID        *string  `json:"lastFeedId"`
	Position          *Point   `json:"position"`
	Altitude          *float64 `json:"altitude"`
	Heading           *float64 `json:"heading"`
	SpeedKnots        *float64 `json:"speedKnots"`
	Course            *float64 `json:"course"`
	MilStd2525dSymbol *string  `json:"milStd2525dSymbol"`
	Affiliation       *string  `json:"affiliation"`
	TrackEnvironment  *string  `json:"trackEnvironment"`
	CountryOfOrigin   *string  `json:"countryOfOrigin"`
	AgeoutState       string   `json:"ageoutState"`
	LastObservedAt    *string  `json:"lastObservedAt"`
	LastSeenAt        *string  `json:"lastSeenAt"`
	Exercise          bool     `json:"exercise"`
}

// Point is the entity-service position shape.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Page is one page of entities with the total.
type Page struct {
	Entities []Entity
	Total    int
}

// Source reads entities for a user.
type Source interface {
	Query(ctx context.Context, u *auth.User, q Query) (Page, error)
}

// HTTPSource calls the entity service.
type HTTPSource struct {
	base   string
	client *http.Client
}

// NewHTTPSource targets the entity service's base URL.
func NewHTTPSource(entityServiceURL string, client *http.Client) *HTTPSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPSource{base: strings.TrimRight(entityServiceURL, "/"), client: client}
}

type pageResponse struct {
	Data  []Entity `json:"data"`
	Total int      `json:"total"`
}

// Query implements Source.
func (s *HTTPSource) Query(ctx context.Context, u *auth.User, q Query) (Page, error) {
	v := url.Values{}
	if q.BBox != nil {
		v.Set("north", strconv.FormatFloat(q.BBox.North, 'f', -1, 64))
		v.Set("south", strconv.FormatFloat(q.BBox.South, 'f', -1, 64))
		v.Set("east", strconv.FormatFloat(q.BBox.East, 'f', -1, 64))
		v.Set("west", strconv.FormatFloat(q.BBox.West, 'f', -1, 64))
	}
	if len(q.Types) > 0 {
		v.Set("types", strings.Join(q.Types, ","))
	}
	if len(q.Feeds) > 0 {
		v.Set("feedIds", strings.Join(q.Feeds, ","))
	}
	v.Set("page", strconv.Itoa(max(q.Page, 1)))
	v.Set("pageSize", strconv.Itoa(max(q.PageSize, 1)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/api/v1/entities?"+v.Encode(), nil)
	if err != nil {
		return Page{}, err
	}
	for k, vals := range proxy.IdentityHeaders(u) {
		req.Header[k] = vals
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("entity service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Page{}, fmt.Errorf("entity service answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var pr pageResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return Page{}, fmt.Errorf("entity service: decoding: %w", err)
	}
	return Page{Entities: pr.Data, Total: pr.Total}, nil
}
