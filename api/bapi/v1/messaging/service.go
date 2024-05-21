package messaging

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/twilio"
	"clerk/pkg/jobs"
	clerksentry "clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/twilio/twilio-go/client"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db        database.Database
	clock     clockwork.Clock
	gueClient *gue.Client
	validator *validator.Validate

	// repositories
	instanceRepo       *repository.Instances
	smsCountryTierRepo *repository.SMSCountryTiers
	smsMessageRepo     *repository.SMSMessage
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:        deps.DB(),
		clock:     deps.Clock(),
		gueClient: deps.GueClient(),
		validator: validator.New(),

		instanceRepo:       repository.NewInstances(),
		smsCountryTierRepo: repository.NewSMSCountryTiers(),
		smsMessageRepo:     repository.NewSMSMessage(),
	}
}

const (
	MessageSidParam    = "MessageSid"
	MessageStatusParam = "MessageStatus"
	ErrorCodeParam     = "ErrorCode"
)

type SMSStatusCallbackParams struct {
	MessageSid    string `validate:"required"`
	MessageStatus string `validate:"required"`
	ErrorCode     string
}

// Assuming we do not care about read receipts
var (
	SMSTerminalStatuses = set.New(
		constants.SMSMessageStatusCanceled,
		constants.SMSMessageStatusFailed,
		constants.SMSMessageStatusDelivered,
		constants.SMSMessageStatusUndelivered,
		constants.SMSMessageStatusRead,
		constants.SMSMessageStatusError,
	)
)

func (s *Service) TwilioSMSStatusCallback(ctx context.Context, params map[string]string, signature, traceIDEncoded string) apierror.Error {
	// We need to load the sms_message & instance first,
	// to be able to determine the auth_token for signature verification

	smsStatusCallbackParams := parseParams(params)

	if err := s.validator.Struct(smsStatusCallbackParams); err != nil {
		clerksentry.CaptureException(ctx, fmt.Errorf("twilio/sms_status_callback: parameter validation failed %w", err))
		return apierror.FormValidationFailed(err)
	}

	smsMessage, err := s.smsMessageRepo.QueryBySMSSid(ctx, s.db, smsStatusCallbackParams.MessageSid)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if smsMessage == nil {
		clerksentry.CaptureException(ctx, fmt.Errorf("twilio/sms_status_callback: Could not find sms_message with sms_sid %s", smsStatusCallbackParams.MessageSid))
		return apierror.ResourceNotFound()
	}

	instance, err := s.instanceRepo.FindByID(ctx, s.db, smsMessage.InstanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	twilioConfig := twilio.NewConfigFor(smsMessage, instance)

	// TODO(twilio, mark) double-check full twilioConfig against incoming webhook?

	err = validateTwilioSignature(params, twilioConfig, traceIDEncoded, signature)
	if err != nil {
		clerksentry.CaptureException(ctx, fmt.Errorf("twilio/sms_status_callback: signature validation failed %w", err))
		return apierror.InvalidRequestBody(err)
	}

	// return early if the sms_message is already in a terminal state
	// can happen if webhooks are received out of sequence

	if SMSTerminalStatuses.Contains(constants.SMSMessageStatus(smsMessage.Status)) {
		log.Debug(ctx, "twilio/sms_status_callback: ignoring received status %s for sms_message in terminal status %s", smsStatusCallbackParams.MessageStatus, smsMessage.Status)
		return nil
	}

	// proceed with sms_message update

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		updateCols := set.New[string](
			sqbmodel.SMSMessageColumns.Status,
		)

		smsMessage.Status = smsStatusCallbackParams.MessageStatus

		if smsStatusCallbackParams.ErrorCode != "" {
			smsMessage.Error = null.StringFrom(smsStatusCallbackParams.ErrorCode)
			updateCols.Insert(sqbmodel.SMSMessageColumns.Error)
		}

		err = s.smsMessageRepo.Update(ctx, s.db, smsMessage, updateCols.Array()...)
		if err != nil {
			return true, fmt.Errorf("twilio/sms_status_callback: updating sms_message %s: %w", smsMessage.ID, err)
		}

		// if incoming status is delivered, then also report usage to stripe

		if smsStatusCallbackParams.MessageStatus == string(constants.SMSMessageStatusDelivered) {
			err := s.reportUsage(ctx, tx, smsMessage)
			if err != nil {
				return true, fmt.Errorf("twilio/sms_status_callback: reporting usage to stripe for sms_message %s: %w", smsMessage.ID, err)
			}
		}

		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}

func parseParams(params map[string]string) *SMSStatusCallbackParams {
	return &SMSStatusCallbackParams{
		MessageSid:    params[MessageSidParam],
		MessageStatus: params[MessageStatusParam],
		ErrorCode:     params[ErrorCodeParam],
	}
}

// validateTwilioSignature uses:
// * the callback URL
// * the auth token of a given twilio account (with fallback to previous auth_token values if available)
// * the full params (caution: not only the ones we care about)
// * the signature
// to verify that the webhook indeed comes from twilio
func validateTwilioSignature(params map[string]string, config twilio.Config, traceIDEncoded, signature string) error {
	url := config.GetSMSStatusCallbackURLEncoded(traceIDEncoded)

	for _, authToken := range config.AuthTokens {
		requestValidator := client.NewRequestValidator(authToken)

		if requestValidator.Validate(url, params, signature) {
			return nil
		}
	}

	return ErrInvalidTwilioSignature
}

func (s *Service) reportUsage(ctx context.Context, tx database.Tx, smsMessage *model.SMSMessage) error {
	countryTier, err := s.smsCountryTierRepo.ChooseAggregationTypeBasedOnCountry(ctx, tx, smsMessage.Iso3166Alpha2CountryCode.String)
	if err != nil {
		return err
	}

	return jobs.RegisterDailyActivity(ctx, s.gueClient, jobs.RegisterDailyActivityArgs{
		InstanceID:   smsMessage.InstanceID,
		ResourceID:   smsMessage.ID,
		ResourceType: countryTier,
		Day:          s.clock.Now().UTC(),
	}, jobs.WithTx(tx))
}
