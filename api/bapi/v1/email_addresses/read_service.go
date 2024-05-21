package email_addresses

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
)

// Read - return the payload for an email_address by id
func (s *Service) Read(ctx context.Context, emailAddressID string) (*serialize.EmailAddressResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	emailAddress, apiErr := s.getEmailAddressInInstance(ctx, env.Instance.ID, emailAddressID)
	if apiErr != nil {
		return nil, apiErr
	}

	emailAddressSerializable, err := s.serializableService.ConvertIdentification(ctx, s.db, emailAddress)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.IdentificationEmailAddress(emailAddressSerializable), nil
}
