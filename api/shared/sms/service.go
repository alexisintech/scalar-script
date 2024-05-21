package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/events"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	clerkstrings "clerk/pkg/strings"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/dongri/phonenumber"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

var (
	otpSlugs = set.New(constants.VerificationCodeSlug, constants.ResetPasswordCodeSlug)
)

type Service struct {
	clock              clockwork.Clock
	gueClient          *gue.Client
	eventService       *events.Service
	smsCountryTierRepo *repository.SMSCountryTiers
	smsMessageRepo     *repository.SMSMessage
	subscriptionRepo   *repository.Subscriptions
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:              deps.Clock(),
		gueClient:          deps.GueClient(),
		eventService:       events.NewService(deps),
		smsCountryTierRepo: repository.NewSMSCountryTiers(),
		smsMessageRepo:     repository.NewSMSMessage(),
		subscriptionRepo:   repository.NewSubscriptions(),
	}
}

func (s *Service) Send(ctx context.Context, tx database.Tx, smsData *model.SMSMessageData, env *model.Env) (*model.SMSMessage, error) {
	if env.Instance.IsDevelopmentOrStaging() && !cenv.IsBeforeCutoff(cenv.StopDevInProdCutOffDateEpochTime, env.Instance.CreatedAt) {
		smsData.PrependTagToMessage(env.Instance.EnvironmentType)
	}

	msg, err := newSMSMessage(smsData, env)
	if err != nil {
		return nil, err
	}

	isDevMonthlySMSLimitExceeded, err := s.isDevMonthlySMSLimitExceeded(ctx, tx, env, msg)
	if err != nil {
		return nil, err
	}

	if isDevMonthlySMSLimitExceeded {
		// reject for OTP
		if msg.Slug.Valid && otpSlugs.Contains(msg.Slug.String) {
			return nil, apierror.DevMonthlySMSLimitExceeded(env.Instance.Communication.GetDevMonthlySMSLimit())
		}

		// don't enqueue for sending in other cases
		smsData.DeliveredByClerk = false
		msg.DeliveredByClerk = false
	}

	if err := s.smsMessageRepo.Insert(ctx, tx, msg); err != nil {
		return nil, fmt.Errorf("sms/send: error insert %+v: %w", msg, err)
	}

	if err := s.enqueueJob(ctx, tx, msg); err != nil {
		return nil, fmt.Errorf("sms/send: error enqueuing job %+v: %w", msg, err)
	}

	// Also send event & webhook for SMS
	var userID *string
	if smsData.Identification != nil {
		userID = smsData.Identification.UserID.Ptr()
	}

	if err := s.eventService.SMSCreated(ctx, tx, env.Instance, eventPayload(msg), userID); err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and continue
		sentryclerk.CaptureException(ctx, err)
	}

	return msg, nil
}

func newSMSMessage(smsData *model.SMSMessageData, env *model.Env) (*model.SMSMessage, error) {
	message := &model.SMSMessage{
		SMSMessage: &sqbmodel.SMSMessage{
			Slug:             null.StringFromPtr(smsData.Slug),
			InstanceID:       env.AuthConfig.InstanceID,
			Message:          smsData.Message,
			Status:           string(constants.SMSMessageStatusQueued),
			DeliveredByClerk: smsData.DeliveredByClerk,
			VerificationID:   null.StringFromPtr(smsData.VerificationID),
			IsCustomTemplate: smsData.IsCustomTemplate,
		},
	}

	if smsData.Data != nil {
		dataJSON, err := json.Marshal(smsData.Data)
		if err != nil {
			return nil, clerkerrors.WithStacktrace("sms/send: JSON marshalling failed: %w", err)
		}

		message.Data = dataJSON
	}

	if smsData.Identification != nil {
		phoneNumber := smsData.Identification.PhoneNumber()
		if phoneNumber == nil {
			return nil, clerkerrors.WithStacktrace("sms/send: expected phone number identification, found %s instead",
				smsData.Identification.Type)
		}

		message.PhoneNumberID = null.StringFrom(smsData.Identification.ID)
		message.UserID = smsData.Identification.UserID
		message.ToPhoneNumber = *phoneNumber
	} else if smsData.ToPhoneNumber != nil {
		message.ToPhoneNumber = *smsData.ToPhoneNumber
	}

	iso3166 := phonenumber.GetISO3166ByNumber(strings.TrimLeft(message.ToPhoneNumber, "+"), true)
	message.Iso3166Alpha2CountryCode = null.StringFrom(iso3166.Alpha2)

	if twilioServiceSid := messagingServiceSid(env.Instance); twilioServiceSid != "" {
		message.MessagingServiceSid = null.StringFrom(twilioServiceSid)
	}
	message.FromPhoneNumber = fromNumber(message.ToPhoneNumber, message.IsCustomTemplate, env.Instance)

	return message, nil
}

func messagingServiceSid(instance *model.Instance) string {
	// Sub-account is being used
	if instance.Communication.TwilioAccountSID.Valid {
		return instance.Communication.TwilioMessagingServiceSID.String
	}
	// If a Messaging Service SID is configured for the main account return it
	return cenv.Get(cenv.TwilioMessagingServiceSID)
}

// fromNumber returns the sender number that will be used.
// The introduction of Messaging Service SID setting removes the need for added complexity here
// and this functionality will be deprecated.
func fromNumber(toNumber string, basedOnCustomTemplate bool, instance *model.Instance) string {
	if instance.Communication.TwilioFromSMSPhoneNumber.Valid {
		return instance.Communication.TwilioFromSMSPhoneNumber.String
	}
	var fromPhoneNumber string
	if !instance.IsProduction() && cenv.IsEnabled(cenv.FlagUseTwilioDevInstances) {
		fromPhoneNumber = cenv.Get(cenv.TwilioPhoneNumberDevInstances)
	} else if basedOnCustomTemplate && cenv.IsEnabled(cenv.FlagUseTwilioCustomTemplates) {
		fromPhoneNumber = cenv.Get(cenv.TwilioPhoneNumberCustomTemplates)
	} else {
		fromPhoneNumber = cenv.Get(cenv.TwilioPhoneNumber)
	}
	destination := phonenumber.GetISO3166ByNumber(strings.TrimLeft(toNumber, "+"), true)

	nonIntlNumbers := cenv.GetStringList(cenv.TwilioLocalCountryPhoneNumbers)
	if len(nonIntlNumbers) == 0 {
		return fromPhoneNumber
	}
	for _, sender := range nonIntlNumbers {
		if phonenumber.GetISO3166ByNumber(strings.TrimLeft(sender, "+"), true).CountryCode == destination.CountryCode {
			return sender
		}
	}
	return fromPhoneNumber
}

func (s *Service) enqueueJob(ctx context.Context, tx database.Tx, sms *model.SMSMessage) error {
	if !sms.DeliveredByClerk {
		return nil
	}

	return jobs.SendSMS(ctx, s.gueClient, jobs.SendSMSArgs{
		InstanceID: sms.InstanceID,
		SmsID:      sms.ID,
	}, jobs.WithTx(tx))
}

// isDevMonthlySMSLimitExceeded checks if the monthly limit of allowed Clerk-delivered SMS for a dev instance has been reached
func (s *Service) isDevMonthlySMSLimitExceeded(ctx context.Context, tx database.Tx, env *model.Env, msg *model.SMSMessage) (bool, error) {
	if env.Instance.IsProduction() {
		return false, nil
	}

	if !msg.DeliveredByClerk {
		return false, nil
	}

	// US numbers (including test numbers +1 (XXX) 555 01YY) are excluded from limit
	if strings.HasPrefix(msg.ToPhoneNumber, "+1") {
		return false, nil
	}

	// Do not enforce limit if app has a paid subscription
	if env.Subscription.StripeSubscriptionID.Valid {
		return false, nil
	}

	now := s.clock.Now()
	count, err := s.smsMessageRepo.CountMonthlyForDev(ctx, tx, env.Instance, now)
	if err != nil {
		return false, err
	}

	// compare with monthly limit for dev instances
	return int(count) >= env.Instance.Communication.GetDevMonthlySMSLimit(), nil
}

// FIXME
// This is a a duplicate of:
// pkg/serialize/sms_message
// However using it here introduces an import cycle

type emailEventPayload struct {
	Object           string          `json:"object"`
	ID               string          `json:"id"`
	Slug             null.String     `json:"slug"`
	FromPhoneNumber  string          `json:"from_phone_number"`
	ToPhoneNumber    string          `json:"to_phone_number"`
	PhoneNumberID    *string         `json:"phone_number_id"`
	UserID           null.String     `json:"user_id,omitempty"`
	Message          string          `json:"message"`
	Status           string          `json:"status"`
	Data             json.RawMessage `json:"data"`
	DeliveredByClerk bool            `json:"delivered_by_clerk"`
}

func eventPayload(sms *model.SMSMessage) *emailEventPayload {
	return &emailEventPayload{
		Object:           "sms_message",
		ID:               sms.ID,
		Slug:             sms.Slug,
		FromPhoneNumber:  sms.FromPhoneNumber,
		ToPhoneNumber:    sms.ToPhoneNumber,
		PhoneNumberID:    clerkstrings.ToStringPtr(&sms.PhoneNumberID),
		UserID:           sms.UserID,
		Message:          sms.Message,
		Status:           sms.Status,
		Data:             json.RawMessage(sms.Data),
		DeliveredByClerk: sms.DeliveredByClerk,
	}
}
