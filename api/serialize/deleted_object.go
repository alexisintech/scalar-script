package serialize

type DeletedObjectResponse struct {
	ID      string `json:"id,omitempty"`
	Slug    string `json:"slug,omitempty"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

func DeletedObject(id, object string) *DeletedObjectResponse {
	return deletedObject(id, "", object)
}

func deletedObject(id, slug, object string) *DeletedObjectResponse {
	return &DeletedObjectResponse{
		ID:      id,
		Slug:    slug,
		Object:  object,
		Deleted: true,
	}
}
