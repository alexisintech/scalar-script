package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/time"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type SessionClientResponse struct {
	Object                   string               `json:"object"`
	ID                       string               `json:"id"`
	Status                   string               `json:"status"`
	ExpireAt                 int64                `json:"expire_at"`
	AbandonAt                int64                `json:"abandon_at"`
	LastActiveAt             int64                `json:"last_active_at"`
	LatestActivity           interface{}          `json:"latest_activity,omitempty"`
	LastActiveOrganizationID *string              `json:"last_active_organization_id"`
	Actor                    null.JSON            `json:"actor,omitempty"`
	User                     *sessionUserResponse `json:"user"`
	PublicUserData           *publicUserData      `json:"public_user_data" logger:"omit"`
	CreatedAt                int64                `json:"created_at"`
	UpdatedAt                int64                `json:"updated_at"`

	// NOTE: This is only populated for responses to `/v1/client`
	Token *TokenResponse `json:"last_active_token"`
}

type publicUserData struct {
	FirstName  *string `json:"first_name"`
	LastName   *string `json:"last_name"`
	ImageURL   string  `json:"image_url,omitempty"`
	HasImage   bool    `json:"has_image"`
	Identifier string  `json:"identifier"`

	// DEPRECATED: After 4.36.0
	ProfileImageURL string `json:"profile_image_url"`
}

type sessionActivityResponse struct {
	Object         string  `json:"object"`
	ID             string  `json:"id"`
	DeviceType     *string `json:"device_type,omitempty"`
	IsMobile       bool    `json:"is_mobile"`
	BrowserName    *string `json:"browser_name,omitempty"`
	BrowserVersion *string `json:"browser_version,omitempty"`
	IPAddress      *string `json:"ip_address,omitempty"`
	City           *string `json:"city,omitempty"`
	Country        *string `json:"country,omitempty"`
}

type SessionServerResponse struct {
	Object                   string    `json:"object"`
	ID                       string    `json:"id"`
	ClientID                 string    `json:"client_id"`
	UserID                   string    `json:"user_id"`
	Status                   string    `json:"status"`
	LastActiveOrganizationID string    `json:"last_active_organization_id,omitempty"`
	Actor                    null.JSON `json:"actor,omitempty"`
	LastActiveAt             int64     `json:"last_active_at"`
	ExpireAt                 int64     `json:"expire_at"`
	AbandonAt                int64     `json:"abandon_at"`
	CreatedAt                int64     `json:"created_at"`
	UpdatedAt                int64     `json:"updated_at"`
}

func SessionToServerAPI(clock clockwork.Clock, session *model.Session) *SessionServerResponse {
	return &SessionServerResponse{
		Object:                   "session",
		ID:                       session.ID,
		ClientID:                 session.ClientID,
		UserID:                   session.UserID,
		Status:                   session.GetStatus(clock),
		LastActiveAt:             time.UnixMilli(session.TouchedAt),
		LastActiveOrganizationID: session.ActiveOrganizationID.String,
		Actor:                    session.Actor,
		ExpireAt:                 time.UnixMilli(session.ExpireAt),
		AbandonAt:                time.UnixMilli(session.AbandonAt),
		CreatedAt:                time.UnixMilli(session.CreatedAt),
		UpdatedAt:                time.UnixMilli(session.UpdatedAt),
	}
}

func SessionToClientAPI(ctx context.Context, clock clockwork.Clock, session *model.SessionWithUser) (*SessionClientResponse, error) {
	response := sessionToClientAPI(clock, session.Session)

	response.PublicUserData = &publicUserData{
		FirstName:       session.User.FirstName.Ptr(),
		LastName:        session.User.LastName.Ptr(),
		ProfileImageURL: session.User.ProfileImageURL,
		Identifier:      session.Identifier,
		ImageURL:        session.User.ImageURL,
		HasImage:        session.User.User.ProfileImagePublicURL.Valid,
	}

	response.User = sessionUser(ctx, session)

	return response, nil
}

func SessionWithActivityToClientAPI(clock clockwork.Clock, session *model.SessionWithActivity) *SessionClientResponse {
	resp := sessionToClientAPI(clock, session.Session)

	if session.LatestActivity != nil {
		resp.LatestActivity = &sessionActivityResponse{
			Object:         "session_activity",
			ID:             session.LatestActivity.ID,
			DeviceType:     session.LatestActivity.DeviceType.Ptr(),
			IsMobile:       session.LatestActivity.IsMobile,
			BrowserName:    session.LatestActivity.BrowserName.Ptr(),
			BrowserVersion: session.LatestActivity.BrowserVersion.Ptr(),
			IPAddress:      session.LatestActivity.IPAddress.Ptr(),
			City:           session.LatestActivity.City.Ptr(),
			Country:        session.LatestActivity.Country.Ptr(),
		}
	}

	return resp
}

func sessionToClientAPI(clock clockwork.Clock, session *model.Session) *SessionClientResponse {
	return &SessionClientResponse{
		Object:                   "session",
		ID:                       session.ID,
		Status:                   session.GetStatus(clock),
		LastActiveAt:             time.UnixMilli(session.TouchedAt),
		LastActiveOrganizationID: session.ActiveOrganizationID.Ptr(),
		Actor:                    session.Actor,
		ExpireAt:                 time.UnixMilli(session.ExpireAt),
		AbandonAt:                time.UnixMilli(session.AbandonAt),
		CreatedAt:                time.UnixMilli(session.CreatedAt),
		UpdatedAt:                time.UnixMilli(session.UpdatedAt),
	}
}
