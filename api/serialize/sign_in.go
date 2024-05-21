package serialize

import (
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/oauth"
	"clerk/pkg/set"
	"clerk/pkg/time"
	usersettings "clerk/pkg/usersettings/clerk"

	"github.com/jonboulle/clockwork"
)

type SignInResponse struct {
	Object                 string               `json:"object"`
	ID                     string               `json:"id"`
	Status                 string               `json:"status"`
	SupportedIdentifiers   []string             `json:"supported_identifiers"`
	SupportedFirstFactors  []model.SignInFactor `json:"supported_first_factors"`
	SupportedSecondFactors []model.SignInFactor `json:"supported_second_factors"`

	FirstFactorVerification  *VerificationResponse `json:"first_factor_verification"`
	SecondFactorVerification *VerificationResponse `json:"second_factor_verification"`

	Identifier       *string   `json:"identifier"`
	UserData         *userData `json:"user_data" logger:"omit"`
	CreatedSessionID *string   `json:"created_session_id"`
	AbandonAt        int64     `json:"abandon_at"`
}

// userData is data that we expose during the sign-in process.
// All you need to know is someone's "identifier" to expose this information.
type userData struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	ImageURL  string  `json:"image_url,omitempty"`
	HasImage  bool    `json:"has_image"`

	// DEPRECATED: After 4.36.0
	ProfileImageURL string `json:"profile_image_url"`
}

func SignIn(clock clockwork.Clock, signIn *model.SignInSerializable, userSettings *usersettings.UserSettings) (*SignInResponse, error) {
	status := signIn.SignIn.Status(clock)
	signInResponse := SignInResponse{
		Object:               "sign_in_attempt",
		ID:                   signIn.SignIn.ID,
		AbandonAt:            time.UnixMilli(signIn.SignIn.AbandonAt),
		Status:               status,
		SupportedIdentifiers: make([]string, 0),
	}

	// supported identifiers
	for _, identifier := range set.SortedStringSet(userSettings.IdentificationStrategies()) {
		if !oauth.ProviderExists(identifier) {
			signInResponse.SupportedIdentifiers = append(signInResponse.SupportedIdentifiers, identifier)
		}
	}

	// identification
	if signIn.Identification != nil {
		identification := signIn.Identification

		switch identification.Type {
		case constants.ITEmailAddress:
			signInResponse.Identifier = identification.EmailAddress()
		case constants.ITPhoneNumber:
			signInResponse.Identifier = identification.PhoneNumber()
		case constants.ITUsername:
			signInResponse.Identifier = identification.Username()
		case constants.ITWeb3Wallet:
			signInResponse.Identifier = identification.Web3Wallet()
		default:
			// OAuth
			signInResponse.Identifier = identification.EmailAddress()
		}
	}

	signInResponse.SupportedFirstFactors = signIn.SupportedFirstFactors

	if signIn.FirstFactorVerification != nil {
		signInResponse.FirstFactorVerification = Verification(signIn.FirstFactorVerification)
	}

	if signIn.User != nil && !userSettings.AttackProtection.PII.Enabled {
		signInResponse.UserData = &userData{
			FirstName:       signIn.User.FirstName.Ptr(),
			LastName:        signIn.User.LastName.Ptr(),
			ProfileImageURL: signIn.User.ProfileImageURL,
			ImageURL:        signIn.User.ImageURL,
			HasImage:        signIn.User.User.ProfileImagePublicURL.Valid,
		}
	}

	// second factor
	signInResponse.SupportedSecondFactors = signIn.SecondFactors

	if signIn.SecondFactorVerification != nil {
		signInResponse.SecondFactorVerification = Verification(signIn.SecondFactorVerification)
	}

	// created_session
	if signIn.SignIn.CreatedSessionID.Valid {
		signInResponse.CreatedSessionID = &signIn.SignIn.CreatedSessionID.String
	}

	return &signInResponse, nil
}
