package serialize

type PaginatedResponse struct {
	Data       []interface{} `json:"data"`
	TotalCount int64         `json:"total_count"`
}

func Paginated(data []interface{}, totalCount int64) *PaginatedResponse {
	return &PaginatedResponse{
		Data:       data,
		TotalCount: totalCount,
	}
}
