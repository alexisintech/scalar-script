package serialize_test

import (
	"context"
	"encoding/json"
	"testing"

	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"

	"github.com/jonboulle/clockwork"
)

func TestSignUpVerificationsNextAction(t *testing.T) {
	t.Parallel()
	// Dependencies unrelated to this test.
	clock := clockwork.NewFakeClock()
	signUp := &model.SignUp{SignUp: &sqbmodel.SignUp{}}

	// Prepare a model for verification info.
	vws := &model.VerificationWithStatus{
		Verification: &model.Verification{
			Verification: &sqbmodel.Verification{},
		},
	}
	// Get a model with an email code and phone code verification,
	// using the above verification model.
	m := &model.SignUpSerializable{
		EmailAddressVerification: vws,
		PhoneNumberVerification:  vws,
		SignUp:                   signUp,
	}

	// These structs help with the assertions.
	type nextAction struct {
		NextAction string `json:"next_action"`
	}
	type expectedStruct struct {
		Verifications struct {
			EmailAddress *nextAction `json:"email_address"`
			PhoneNumber  *nextAction `json:"phone_number"`
		} `json:"verifications"`
	}
	var res expectedStruct

	// Test matrix. For each verification strategy, test all possible
	// verification status values.
	for _, strategy := range []struct {
		name    string
		isValid bool
	}{
		{constants.VSEmailCode, true},
		{constants.VSPhoneCode, true},
		{constants.VSAdmin, false},
		{constants.VSFromMock, false},
		{constants.VSPassword, false},
	} {
		for _, tc := range []struct {
			status string
			want   string
		}{
			{constants.VERVerified, ""},
			{constants.VERFailed, "needs_prepare"},
			{constants.VERExpired, "needs_prepare"},
			{constants.VERUnverified, "needs_attempt"},
			{constants.VERTransferable, ""},
			{"", "needs_prepare"},
		} {
			vws.Strategy = strategy.name
			vws.Status = tc.status

			resp, err := serialize.SignUp(context.Background(), clock, m)
			if err != nil {
				t.Errorf("strategy %s: expected no error, got %s", strategy.name, err)
			}
			if !strategy.isValid {
				continue
			}
			enc, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			err = json.Unmarshal(enc, &res)
			if err != nil {
				t.Fatal(err)
			}
			got := res.Verifications.EmailAddress.NextAction
			if tc.want != got {
				t.Errorf("strategy %s:\nwant\n%s\ngot\n%s\n", strategy.name, tc.want, got)
			}
			got = res.Verifications.PhoneNumber.NextAction
			if tc.want != got {
				t.Errorf("strategy %s:\nwant\n%s\ngot\n%s\n", strategy.name, tc.want, got)
			}
		}
	}
}

func TestSignUpVerificationsSupportedStrategies(t *testing.T) {
	t.Parallel()
	// Dependencies unrelated to this test.
	clock := clockwork.NewFakeClock()
	signUp := &model.SignUp{SignUp: &sqbmodel.SignUp{}}

	// Prepare a model for verification info.
	vws := &model.VerificationWithStatus{
		Verification: &model.Verification{
			Verification: &sqbmodel.Verification{},
		},
	}
	// Get a model with an email code and phone code verification,
	// using the above verification model.
	m := &model.SignUpSerializable{
		EmailAddressVerification: vws,
		PhoneNumberVerification:  vws,
		SignUp:                   signUp,
	}

	// These structs help with the assertions.
	type supportedStrategies struct {
		SupportedStrategies []string `json:"supported_strategies"`
	}
	type expectedStruct struct {
		Verifications struct {
			EmailAddress *supportedStrategies `json:"email_address"`
			PhoneNumber  *supportedStrategies `json:"phone_number"`
		} `json:"verifications"`
	}
	var res expectedStruct

	// Test matrix. For each verification strategy, test all possible
	// verification status values.
	for _, strategy := range []struct {
		name    string
		isValid bool
	}{
		{constants.VSEmailCode, true},
		{constants.VSPhoneCode, true},
		{constants.VSAdmin, false},
		{constants.VSFromMock, false},
		{constants.VSPassword, false},
	} {
		vws.Strategy = strategy.name
		want := []string{strategy.name}

		resp, err := serialize.SignUp(context.Background(), clock, m)
		if err != nil {
			t.Errorf("strategy %s: expected no error, got %s", strategy.name, err)
		}
		if !strategy.isValid {
			continue
		}

		enc, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		err = json.Unmarshal(enc, &res)
		if err != nil {
			t.Fatal(err)
		}
		got := res.Verifications.EmailAddress.SupportedStrategies
		if len(got) != 1 || want[0] != got[0] {
			t.Errorf("strategy %s:\nwant\n%v\ngot\n%v\n", strategy.name, want, got)
		}
		got = res.Verifications.PhoneNumber.SupportedStrategies
		if len(got) != 1 || want[0] != got[0] {
			t.Errorf("strategy %s:\nwant\n%v\ngot\n%v\n", strategy.name, want, got)
		}
	}
}
