// Google Analytics Measurement Protocol (GAMP) support
// * Universal Analytics
// * Google Analytics v4

package gamp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"clerk/model"
	"clerk/pkg/ctx/request_info"
	"clerk/pkg/ctx/tracking"
	"clerk/pkg/jobs"
	cstrings "clerk/pkg/strings"
	"clerk/pkg/time"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type EventName string

const (
	baseURL = "https://www.google-analytics.com"

	SignUpEvent = EventName("sign_up")
	LoginEvent  = EventName("login")
)

type Service struct {
	clock           clockwork.Clock
	gueClient       *gue.Client
	integrationRepo *repository.Integrations
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:           deps.Clock(),
		gueClient:       deps.GueClient(),
		integrationRepo: repository.NewIntegrations(),
	}
}

func (s *Service) EnqueueEvent(ctx context.Context, exec database.Executor, instanceID, userID string, eventName EventName, method string) error {
	trackingData, ok := tracking.FromContext(ctx)
	if !ok || trackingData == nil {
		return nil
	}

	// Abort if no GA cookies found
	if len(trackingData.GoogleAnalyticsCookies) == 0 {
		return nil
	}

	// Abort if no Google Client ID (i.e. no GA cookie detected)
	gaClientID := trackingData.GAClientID()
	if gaClientID == "" {
		return nil
	}

	integration, err := s.integrationRepo.QueryByInstanceIDAndType(ctx, exec, instanceID, model.GoogleAnalyticsIntegration)
	if err != nil {
		return fmt.Errorf("gamp/enqueue: querying integrations by instance %s and type %s: %w",
			instanceID, model.GoogleAnalyticsIntegration, err)
	}

	// Abort if no integration record
	if integration == nil {
		return nil
	}

	// Abort if there is no subscription for this event
	gaIntegrationMetadata := &model.GoogleAnalyticsIntegrationMetadata{}
	err = json.Unmarshal(integration.Metadata, gaIntegrationMetadata)
	if err != nil {
		return err
	}

	if !cstrings.ArrayContains(gaIntegrationMetadata.Events, string(eventName)) {
		return nil
	}

	// Extract also the session ID if available
	gaSessionID := trackingData.GASessionID(gaIntegrationMetadata.MeasurementID)

	requestInfo := request_info.FromContext(ctx)

	gaEventMetadata := jobs.GoogleAnalyticsEventMetadata{
		GoogleAnalyticsClientID:  gaClientID,
		GoogleAnalyticsSessionID: gaSessionID,
		EventName:                string(eventName),
		TimestampMicros:          time.MicroSeconds(s.clock.Now()),
		UserAgent:                requestInfo.UserAgent,
		Origin:                   requestInfo.Origin,
		RemoteAddr:               requestInfo.RemoteAddr,
		UserID:                   userID,
		Method:                   method,
	}

	err = jobs.SendGoogleAnalyticsEvent(ctx, s.gueClient,
		jobs.GoogleAnalyticsArgs{IntegrationID: integration.ID, GoogleAnalyticsEventMetadata: gaEventMetadata})
	if err != nil {
		return err
	}

	return nil
}

func SendEvent(gaIntegrationMetadata *model.GoogleAnalyticsIntegrationMetadata, gaEventMetadata *jobs.GoogleAnalyticsEventMetadata) error {
	switch gaIntegrationMetadata.GoogleAnalyticsType {
	case string(model.GoogleAnalyticsUniversal):
		return sendUniversalHit(gaIntegrationMetadata, gaEventMetadata)
	case string(model.GoogleAnalyticsV4):
		return sendGA4Event(gaIntegrationMetadata, gaEventMetadata)
	default:
		return fmt.Errorf("unsupported Google Analytics type")
	}
}

func ipFromRemoteAddr(remoteAddr string) string {
	parts := strings.Split(remoteAddr, ":")
	return parts[0]
}
