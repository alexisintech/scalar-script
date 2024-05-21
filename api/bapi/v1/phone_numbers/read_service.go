package phone_numbers

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
)

// Read - return the payload for a phone_number by id
func (s *Service) Read(ctx context.Context, phoneNumberID string) (*serialize.PhoneNumberResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	phoneNumber, apiErr := s.getAndCheckPhoneNumber(ctx, env.Instance.ID, phoneNumberID)
	if apiErr != nil {
		return nil, apiErr
	}

	phoneNumberSerializable, err := s.serializableService.ConvertIdentification(ctx, s.db, phoneNumber)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.IdentificationPhoneNumber(phoneNumberSerializable), nil
}
