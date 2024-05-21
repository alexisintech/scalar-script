package serialize

type TotalCountResponse struct {
	Object     string `json:"object"`
	TotalCount int    `json:"total_count"`
}

func TotalCount(totalCount int) *TotalCountResponse {
	return &TotalCountResponse{
		Object:     "total_count",
		TotalCount: totalCount,
	}
}
