package gamp

import (
	"bytes"
	"encoding/json"
	"net/http"

	"clerk/model"
	"clerk/pkg/jobs"
)

const (
	ga4Path = "/mp/collect"
)

type ga4Payload struct {
	ClientID        string     `json:"client_id"`
	UserID          string     `json:"user_id,omitempty"`
	TimestampMicros int64      `json:"timestamp_micros"`
	Events          []ga4Event `json:"events"`
}

type ga4EventParams struct {
	Method    string `json:"method"`
	SessionID string `json:"session_id,omitempty"`
}

type ga4Event struct {
	Name   string         `json:"name"`
	Params ga4EventParams `json:"params"`
}

func sendGA4Event(gaIntegrationMetadata *model.GoogleAnalyticsIntegrationMetadata, gaEventMetadata *jobs.GoogleAnalyticsEventMetadata) error {
	endpoint := baseURL + ga4Path + "?measurement_id=" + gaIntegrationMetadata.MeasurementID + "&api_secret=" + gaIntegrationMetadata.APISecret

	payload := ga4Payload{
		ClientID:        gaEventMetadata.GoogleAnalyticsClientID,
		TimestampMicros: gaEventMetadata.TimestampMicros,
	}

	event := ga4Event{
		Name: gaEventMetadata.EventName,
		Params: ga4EventParams{
			Method:    gaEventMetadata.Method,
			SessionID: gaEventMetadata.GoogleAnalyticsSessionID,
		},
	}

	payload.Events = []ga4Event{event}

	if gaIntegrationMetadata.IncludeUserID {
		payload.UserID = gaEventMetadata.UserID
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
