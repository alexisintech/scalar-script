package serialize

import (
	"clerk/model"
)

// ObjectImage is the name for organization objects.
const ObjectImage = "image"

type ImageResponse struct {
	Object    string `json:"object,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
}

func Image(image *model.Image) *ImageResponse {
	if image == nil {
		return nil
	}
	return &ImageResponse{
		Object:    ObjectImage,
		ID:        image.ID,
		Name:      image.Name,
		PublicURL: image.GetCDNURL(),
	}
}
