package gamp

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"clerk/model"
	"clerk/pkg/jobs"
)

const (
	universalPath = "/collect"
)

func sendUniversalHit(gaIntegrationMetadata *model.GoogleAnalyticsIntegrationMetadata, gaEventMetadata *jobs.GoogleAnalyticsEventMetadata) error {
	endpoint := baseURL + universalPath

	data := url.Values{}
	data.Set("v", "1")                                            // version
	data.Set("tid", gaIntegrationMetadata.TrackingID)             // tracking id
	data.Set("cid", gaEventMetadata.GoogleAnalyticsClientID)      // anonymous client id
	data.Set("t", "event")                                        // hit type
	data.Set("ec", "Clerk")                                       // event category
	data.Set("ea", gaEventMetadata.EventName)                     // event action
	data.Set("el", gaEventMetadata.Method)                        // event label
	data.Set("uip", ipFromRemoteAddr(gaEventMetadata.RemoteAddr)) // user ip
	data.Set("ua", gaEventMetadata.UserAgent)                     // user agent
	data.Set("ds", "clerk")                                       // data source

	// TODO custom dimension

	if gaIntegrationMetadata.IncludeUserID {
		data.Set("uid", gaEventMetadata.UserID)
	}

	client := &http.Client{}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Content-Length", strconv.Itoa(len(data.Encode())))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
