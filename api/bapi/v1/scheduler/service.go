package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/pkg/externalapis/slack"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	clerktime "clerk/pkg/time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/vgarvardt/gue/v2"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

var (
	httpClient = &http.Client{Timeout: 10 * time.Second}

	cloudflareZoneID                             = cenv.Get(cenv.CloudflareZoneID)
	cloudflareCustomHostnameQuotaAlertPercentage = cenv.GetInt(cenv.CloudflareCustomHostnameQuotaAlertPercentage)
)

type Service struct {
	gueClient *gue.Client
}

func NewService(gueClient *gue.Client) *Service {
	return &Service{
		gueClient: gueClient,
	}
}

func (s *Service) MonitorCustomHostname(ctx context.Context) apierror.Error {
	quota, err := getCustomHostnameQuota(cloudflareZoneID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	usedPercentage := float64(quota.Used) / float64(quota.Allocated) * 100
	if int(usedPercentage) < cloudflareCustomHostnameQuotaAlertPercentage {
		return nil
	}

	err = jobs.SendSlackAlert(ctx, s.gueClient,
		jobs.SlackAlertArgs{
			Webhook: cenv.Get(cenv.SlackSystemsChannelWebhook),
			Message: slack.Message{
				Title: "Cloudflare Custom Hostnames quota exceeded!",
				Text:  fmt.Sprintf("Using %d hostnames out of the %d available (%.2f%%)\n", quota.Used, quota.Allocated, usedPercentage),
				Type:  slack.Warning,
			},
		})
	if err != nil {
		// report error to sentry
		sentryclerk.CaptureException(ctx, err)
	}

	return nil
}

type customHostnameQuotaResponse struct {
	cloudflare.Response
	Result *customHostnameQuota `json:"result"`
}

type customHostnameQuota struct {
	Allocated int `json:"allocated"`
	Used      int `json:"used"`
}

// The endpoint we are using (https://api.cloudflare.com/client/v4/zones/{zoneID}/custom_hostnames/quota) is
// not documented in the CF docs neither is supported from their Go-SDK. I will make an attempt to contribute
// to their Go-SDK and if accepted we can remove all the below code.
func getCustomHostnameQuota(zoneID string) (*customHostnameQuota, error) {
	u, err := url.JoinPath(cloudflareAPI, "zones", zoneID, "custom_hostnames", "quota")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cenv.Get(cenv.CloudflareAPITokenMonitorCustomHostnameQuota))

	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	resp := &customHostnameQuotaResponse{}
	if err = json.NewDecoder(response.Body).Decode(resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("cloudflare: failed to fetch custom hostnames quota %s", resp.Errors[0].Message)
	}

	return resp.Result, err
}

func (s *Service) EnqueueHypeStatsJob(ctx context.Context) apierror.Error {
	// Determine the date to process
	// We'll truncate the date to the beginning of the previous week (ie. "Last Monday").
	// This is so we can trigger this job on any day of the current week, and always process a full week of data.
	t := clerktime.DateTrunc("week", time.Now().UTC()).AddDate(0, 0, -7)

	err := jobs.ProcessHypeStatsPartition(ctx, s.gueClient, jobs.ProcessHypeStatsPartitionArgs{
		// When the API queues this job, LastEvaluatedPartitionKey should always be an empty string
		// to signal that we're starting from the beginning of our list of instances.
		LastEvaluatedPartitionKey: "",
		Date:                      t.Format("2006-01-02"),
		PartitionIndex:            0,
		PartitionSize:             20,
	})

	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}
