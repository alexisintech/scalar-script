package serialize

type InstanceDeployStatusResponse struct {
	Status string `json:"status"`
}

func InstanceDeployStatus(status string) *InstanceDeployStatusResponse {
	return &InstanceDeployStatusResponse{
		Status: status,
	}
}
