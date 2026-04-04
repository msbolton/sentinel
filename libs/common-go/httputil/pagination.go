package httputil

import (
	"net/http"
	"strconv"
)

// Pagination holds parsed pagination query parameters.
type Pagination struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Offset   int `json:"-"`
}

// DefaultPageSize is the default number of items per page.
const DefaultPageSize = 50

// MaxPageSize is the maximum allowed page size.
const MaxPageSize = 500

// ParsePagination extracts page and pageSize from query parameters.
func ParsePagination(r *http.Request) Pagination {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}

	pageSize := queryInt(r, "pageSize", DefaultPageSize)
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	return Pagination{
		Page:     page,
		PageSize: pageSize,
		Offset:   (page - 1) * pageSize,
	}
}

// PaginatedResponse is the standard paginated response shape matching NestJS output.
type PaginatedResponse struct {
	Data     interface{} `json:"data"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

func queryInt(r *http.Request, key string, defaultVal int) int {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}
