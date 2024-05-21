package sms

import (
	"testing"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/strings"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volatiletech/null/v8"
)

func TestNewSMSMessage_WithoutIdentification(t *testing.T) {
	t.Parallel()

	smsData := &model.SMSMessageData{
		Slug:             strings.ToPtr("test"),
		Message:          "hello world",
		DeliveredByClerk: true,
		VerificationID:   strings.ToPtr("ver_222277889999001122334455bWC"),
		ToPhoneNumber:    strings.ToPtr("+441234567890"),
	}
	authConfig := model.AuthConfig{
		AuthConfig: &sqbmodel.AuthConfig{},
	}
	authConfig.InstanceID = "ins_111111111111222222222222F7g"
	env := &model.Env{
		AuthConfig: &authConfig,
		Instance: &model.Instance{
			Instance: &sqbmodel.Instance{
				ID: "ins_111111111111222222222222F7g",
			},
		}}

	sms, err := newSMSMessage(smsData, env)
	require.NoError(t, err)
	assert.Equal(t, &model.SMSMessage{
		SMSMessage: &sqbmodel.SMSMessage{
			InstanceID:               "ins_111111111111222222222222F7g",
			FromPhoneNumber:          cenv.Get(cenv.TwilioPhoneNumber),
			VerificationID:           null.StringFrom("ver_222277889999001122334455bWC"),
			Iso3166Alpha2CountryCode: null.StringFrom("GB"),
			Message:                  "hello world",
			ToPhoneNumber:            "+441234567890",
			DeliveredByClerk:         true,
			Slug:                     null.StringFrom("test"),
			Status:                   "queued",
		},
	}, sms)
}

func TestNewSMSMessage_WithIdentification(t *testing.T) {
	t.Parallel()

	smsData := &model.SMSMessageData{
		Slug:             strings.ToPtr("test"),
		Message:          "hello world",
		DeliveredByClerk: true,
		VerificationID:   strings.ToPtr("ver_222277889999001122334455bWC"),
		Identification: &model.Identification{
			Identification: &sqbmodel.Identification{
				ID:         "idn_222277889999001122334455bDD",
				Identifier: null.StringFrom("+301234567890"),
				Type:       constants.ITPhoneNumber,
				Status:     constants.ISNotSet,
			},
		},
	}
	authConfig := model.AuthConfig{
		AuthConfig: &sqbmodel.AuthConfig{},
	}
	authConfig.InstanceID = "ins_111111111111222222222222F7g"
	env := &model.Env{
		AuthConfig: &authConfig,
		Instance: &model.Instance{
			Instance: &sqbmodel.Instance{
				ID: "ins_111111111111222222222222F7g",
			},
		}}

	sms, err := newSMSMessage(smsData, env)
	require.NoError(t, err)
	assert.Equal(t, &model.SMSMessage{
		SMSMessage: &sqbmodel.SMSMessage{
			InstanceID:               "ins_111111111111222222222222F7g",
			FromPhoneNumber:          cenv.Get(cenv.TwilioPhoneNumber),
			VerificationID:           null.StringFrom("ver_222277889999001122334455bWC"),
			Iso3166Alpha2CountryCode: null.StringFrom("GR"),
			Message:                  "hello world",
			ToPhoneNumber:            "+301234567890",
			DeliveredByClerk:         true,
			Slug:                     null.StringFrom("test"),
			Status:                   "queued",
			PhoneNumberID:            null.StringFrom("idn_222277889999001122334455bDD"),
		},
	}, sms)
}

// nolint:paralleltest
func TestNewSMSMessage_LocalCountryPhoneNumberConfigured(t *testing.T) {
	fromNumberUK := "441234567891"
	t.Setenv(cenv.TwilioLocalCountryPhoneNumbers, fromNumberUK)

	smsData := &model.SMSMessageData{
		Slug:             strings.ToPtr("test"),
		Message:          "hello world",
		DeliveredByClerk: true,
		VerificationID:   strings.ToPtr("ver_222277889999001122334455bWC"),
		Identification: &model.Identification{
			Identification: &sqbmodel.Identification{
				ID:         "idn_222277889999001122334455bDD",
				Identifier: null.StringFrom("+441234567890"),
				Type:       constants.ITPhoneNumber,
				Status:     constants.ISNotSet,
			},
		},
	}
	authConfig := model.AuthConfig{
		AuthConfig: &sqbmodel.AuthConfig{},
	}
	authConfig.InstanceID = "ins_111111111111222222222222F7g"
	env := &model.Env{
		AuthConfig: &authConfig,
		Instance: &model.Instance{
			Instance: &sqbmodel.Instance{
				ID: "ins_111111111111222222222222F7g",
			},
		}}

	sms, err := newSMSMessage(smsData, env)
	require.NoError(t, err)
	assert.Equal(t, &model.SMSMessage{
		SMSMessage: &sqbmodel.SMSMessage{
			InstanceID:               "ins_111111111111222222222222F7g",
			FromPhoneNumber:          "441234567891",
			VerificationID:           null.StringFrom("ver_222277889999001122334455bWC"),
			Iso3166Alpha2CountryCode: null.StringFrom("GB"),
			Message:                  "hello world",
			ToPhoneNumber:            "+441234567890",
			DeliveredByClerk:         true,
			Slug:                     null.StringFrom("test"),
			Status:                   "queued",
			PhoneNumberID:            null.StringFrom("idn_222277889999001122334455bDD"),
		},
	}, sms)
}
