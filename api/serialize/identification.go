package serialize

import (
	"fmt"
	"sort"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/time"
)

type EmailAddressResponse struct {
	ID           string                         `json:"id"`
	Object       string                         `json:"object"`
	EmailAddress string                         `json:"email_address"`
	Reserved     bool                           `json:"reserved"`
	Verification *VerificationResponse          `json:"verification"`
	LinkedTo     []LinkedIdentificationResponse `json:"linked_to"`
	CreatedAt    int64                          `json:"created_at"`
	UpdatedAt    int64                          `json:"updated_at"`
}

type PhoneNumberResponse struct {
	ID                      string                         `json:"id"`
	Object                  string                         `json:"object"`
	PhoneNumber             string                         `json:"phone_number"`
	ReservedForSecondFactor bool                           `json:"reserved_for_second_factor"`
	DefaultSecondFactor     bool                           `json:"default_second_factor"`
	Reserved                bool                           `json:"reserved"`
	Verification            *VerificationResponse          `json:"verification"`
	LinkedTo                []LinkedIdentificationResponse `json:"linked_to"`
	BackupCodes             []string                       `json:"backup_codes"`
	CreatedAt               int64                          `json:"created_at"`
	UpdatedAt               int64                          `json:"updated_at"`
}

type Web3WalletResponse struct {
	ID           string                `json:"id"`
	Object       string                `json:"object"`
	Web3Wallet   string                `json:"web3_wallet"`
	Verification *VerificationResponse `json:"verification"`
	CreatedAt    int64                 `json:"created_at"`
	UpdatedAt    int64                 `json:"updated_at"`
}

// Passkeys do not have an identifier that the end user inputs like email or phone number
// so we omit a "passkey" struct field
type PasskeyResponse struct {
	ID           string                `json:"id"`
	Object       string                `json:"object"`
	Name         string                `json:"name"`
	LastUsedAt   *int64                `json:"last_used_at,omitempty"`
	Verification *VerificationResponse `json:"verification"`
	CreatedAt    int64                 `json:"created_at"`
	UpdatedAt    int64                 `json:"updated_at"`
}

func Identification(ident *model.IdentificationSerializable) (interface{}, error) {
	switch ident.Type {
	case constants.ITEmailAddress:
		return IdentificationEmailAddress(ident), nil
	case constants.ITPhoneNumber:
		return IdentificationPhoneNumber(ident), nil
	case constants.ITWeb3Wallet:
		return IdentificationWeb3Wallet(ident), nil
	case constants.ITPasskey:
		return IdentificationPasskey(ident), nil
	default:
		return nil, fmt.Errorf("unexpected identification type %s", ident.Type)
	}
}

func emailAddressesForIdentifications(identifications []*model.IdentificationSerializable) []*EmailAddressResponse {
	emails := make([]*EmailAddressResponse, len(identifications))

	sort.Slice(identifications, func(i, j int) bool {
		return identifications[j].CreatedAt.Before(identifications[i].CreatedAt)
	})
	for i, identification := range identifications {
		emails[i] = IdentificationEmailAddress(identification)
	}
	return emails
}

func phoneNumbersForIdentifications(identifications []*model.IdentificationSerializable) []*PhoneNumberResponse {
	phones := make([]*PhoneNumberResponse, len(identifications))

	sort.Slice(identifications, func(i, j int) bool {
		return identifications[j].CreatedAt.Before(identifications[i].CreatedAt)
	})
	for i, identification := range identifications {
		phones[i] = IdentificationPhoneNumber(identification)
	}
	return phones
}

func web3WalletsForIdentifications(identifications []*model.IdentificationSerializable) []*Web3WalletResponse {
	web3Wallets := make([]*Web3WalletResponse, len(identifications))

	sort.Slice(identifications, func(i, j int) bool {
		return identifications[j].CreatedAt.Before(identifications[i].CreatedAt)
	})
	for i, identification := range identifications {
		web3Wallets[i] = IdentificationWeb3Wallet(identification)
	}
	return web3Wallets
}

func passkeysForIdentifications(identifications []*model.IdentificationSerializable) []*PasskeyResponse {
	passkeys := make([]*PasskeyResponse, len(identifications))

	sort.Slice(identifications, func(i, j int) bool {
		return identifications[j].CreatedAt.Before(identifications[i].CreatedAt)
	})
	for i, identification := range identifications {
		passkeys[i] = IdentificationPasskey(identification)
	}
	return passkeys
}

func IdentificationEmailAddress(ident *model.IdentificationSerializable) *EmailAddressResponse {
	response := &EmailAddressResponse{
		ID:           ident.ID,
		Object:       "email_address",
		EmailAddress: *ident.EmailAddress(),
		Reserved:     ident.IsReserved(),
		CreatedAt:    time.UnixMilli(ident.CreatedAt),
		UpdatedAt:    time.UnixMilli(ident.UpdatedAt),
	}

	if ident.Verification != nil {
		response.Verification = Verification(ident.Verification)
	}

	identificationLinkResponses := make([]LinkedIdentificationResponse, len(ident.ParentIdentifications))
	for i := range ident.ParentIdentifications {
		identificationLinkResponses[i] = linkedIdentification(ident.ParentIdentifications[i])
	}

	response.LinkedTo = identificationLinkResponses

	return response
}

func IdentificationPhoneNumber(ident *model.IdentificationSerializable) *PhoneNumberResponse {
	response := &PhoneNumberResponse{
		ID:                      ident.ID,
		Object:                  "phone_number",
		PhoneNumber:             *ident.PhoneNumber(),
		ReservedForSecondFactor: ident.ReservedForSecondFactor,
		DefaultSecondFactor:     ident.DefaultSecondFactor,
		Reserved:                ident.IsReserved(),
		CreatedAt:               time.UnixMilli(ident.CreatedAt),
		UpdatedAt:               time.UnixMilli(ident.UpdatedAt),
	}

	if ident.Verification != nil {
		response.Verification = Verification(ident.Verification)
	}

	identificationLinkResponses := make([]LinkedIdentificationResponse, len(ident.ParentIdentifications))
	for i := range ident.ParentIdentifications {
		identificationLinkResponses[i] = linkedIdentification(ident.ParentIdentifications[i])
	}

	response.LinkedTo = identificationLinkResponses

	return response
}

func IdentificationPhoneNumberWithBackupCodes(ident *model.IdentificationSerializable, backupCodes []string) *PhoneNumberResponse {
	response := IdentificationPhoneNumber(ident)
	response.BackupCodes = backupCodes
	return response
}

func IdentificationWeb3Wallet(ident *model.IdentificationSerializable) *Web3WalletResponse {
	response := &Web3WalletResponse{
		ID:         ident.ID,
		Object:     "web3_wallet",
		Web3Wallet: *ident.Web3Wallet(),
		CreatedAt:  time.UnixMilli(ident.CreatedAt),
		UpdatedAt:  time.UnixMilli(ident.UpdatedAt),
	}

	if ident.Verification != nil {
		response.Verification = Verification(ident.Verification)
	}

	return response
}

func IdentificationPasskey(ident *model.IdentificationSerializable) *PasskeyResponse {
	response := &PasskeyResponse{
		ID:        ident.ID,
		Object:    constants.ITPasskey,
		CreatedAt: time.UnixMilli(ident.CreatedAt),
		UpdatedAt: time.UnixMilli(ident.UpdatedAt),
	}

	if ident.Passkey != nil {
		if ident.Passkey.LastUsedAt.Valid {
			lastUsedAt := ident.Passkey.LastUsedAt.Time.UTC().UnixMilli()
			response.LastUsedAt = &lastUsedAt
		}
		response.Name = ident.Passkey.Name
	}

	if ident.Verification != nil {
		response.Verification = Verification(ident.Verification)
	}

	return response
}
