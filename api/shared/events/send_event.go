package events

import (
	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/events"
	"clerk/utils/database"
	"context"
	"fmt"
)

func (s *Service) ActorTokenIssued(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.ActorTokenResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.ActorTokenIssued,
		Payload:   payload,
	})
}

func (s *Service) EmailCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload interface{},
	userID *string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		UserID:    userID,
		EventType: events.EventTypes.EmailCreated,
		Payload:   payload,
	})
}

func (s *Service) IOSActivityRegistered(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload events.IOSActivityRegisteredPayload) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.IOSActivityRegistered,
		Payload:   payload,
	})
}

func (s *Service) OrganizationCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationCreated,
		Payload:        payload,
		OrganizationID: &payload.ID,
		UserID:         &payload.CreatedBy,
	})
}

func (s *Service) OrganizationDomainCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationDomainResponse,
	organizationID string,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationDomainCreated,
		Payload:        payload,
		OrganizationID: &organizationID,
	})
}

func (s *Service) OrganizationDomainDeleted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.DeletedObjectResponse,
	organizationID string,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationDomainDeleted,
		Payload:        payload,
		OrganizationID: &organizationID,
	})
}

func (s *Service) OrganizationDomainUpdated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationDomainResponse,
	organizationID string,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationDomainUpdated,
		Payload:        payload,
		OrganizationID: &organizationID,
	})
}

func (s *Service) OrganizationInvitationCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationInvitationResponse,
	userID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationInvitationCreated,
		Payload:        payload,
		OrganizationID: &payload.OrganizationID,
		UserID:         &userID,
	})
}

func (s *Service) OrganizationInvitationAccepted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationInvitationResponse,
	userID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationInvitationAccepted,
		Payload:        payload,
		OrganizationID: &payload.OrganizationID,
		UserID:         &userID,
	})
}

func (s *Service) OrganizationInvitationRevoked(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationInvitationResponse,
	userID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationInvitationRevoked,
		Payload:        payload,
		OrganizationID: &payload.OrganizationID,
		UserID:         &userID,
	})
}

func (s *Service) OrganizationMembershipCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationMembershipResponse,
	organizationID string,
	userID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationMembershipCreated,
		Payload:        payload,
		OrganizationID: &organizationID,
		UserID:         &userID,
	})
}

func (s *Service) OrganizationMembershipDeleted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationMembershipResponse,
	organizationID string,
	userID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationMembershipDeleted,
		Payload:        payload,
		OrganizationID: &organizationID,
		UserID:         &userID,
	})
}

func (s *Service) OrganizationMembershipUpdated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationMembershipResponse,
	organizationID string,
	userID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationMembershipUpdated,
		Payload:        payload,
		OrganizationID: &organizationID,
		UserID:         &userID,
	})
}

func (s *Service) OrganizationDeleted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.DeletedObjectResponse,
	userID *string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationDeleted,
		Payload:        payload,
		OrganizationID: &payload.ID,
		UserID:         userID,
	})
}

func (s *Service) OrganizationTapped(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	organizationID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationTapped,
		OrganizationID: &organizationID,
	})
}

func (s *Service) OrganizationUpdated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.OrganizationResponse,
	userID *string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:       instance,
		EventType:      events.EventTypes.OrganizationUpdated,
		Payload:        payload,
		OrganizationID: &payload.ID,
		UserID:         userID,
	})
}

func (s *Service) SAMLConnectionActivated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	samlConnectionID string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:         instance,
		EventType:        events.EventTypes.SAMLConnectionActivated,
		SAMLConnectionID: &samlConnectionID,
	})
}

func (s *Service) SessionCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	session *model.Session) error {
	payload := serialize.SessionToServerAPI(s.clock, session)

	actorID, err := session.ActorID()
	if err != nil {
		return err
	}

	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.SessionCreated,
		Payload:   payload,
		UserID:    &session.UserID,
		ActorID:   actorID,
	})
}

func (s *Service) SessionEnded(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.SessionServerResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.SessionEnded,
		Payload:   payload,
		UserID:    &payload.UserID,
	})
}

func (s *Service) SessionRemoved(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.SessionServerResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.SessionRemoved,
		Payload:   payload,
		UserID:    &payload.UserID,
	})
}

func (s *Service) SessionRevoked(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.SessionServerResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.SessionRevoked,
		Payload:   payload,
		UserID:    &payload.UserID,
	})
}

func (s *Service) SessionTokenCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	session *model.Session) error {
	actorID, err := session.ActorID()
	if err != nil {
		return err
	}

	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.SessionTokenCreated,
		UserID:    &session.UserID,
		ActorID:   actorID,
	})
}

func (s *Service) SessionTouched(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	session *model.Session) error {
	actorID, err := session.ActorID()
	if err != nil {
		return err
	}

	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.SessionTouched,
		Payload:   session,
		UserID:    &session.UserID,
		ActorID:   actorID,
	})
}

func (s *Service) SMSCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload interface{},
	userID *string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		UserID:    userID,
		EventType: events.EventTypes.SMSCreated,
		Payload:   payload,
	})
}

func (s *Service) TokenCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	token string,
	userID string,
	actorID *string) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.TokenCreated,
		Payload:   token,
		UserID:    &userID,
		ActorID:   actorID,
	})
}

func (s *Service) UserCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSerializable *model.UserSerializable) error {
	payload := serialize.UserToServerAPI(ctx, userSerializable)

	err := s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.UserCreated,
		Payload:   payload,
		UserID:    &userSerializable.ID,
	})
	if err != nil {
		return fmt.Errorf("events/sendUserCreated: send user created event for (%+v, %+v): %w", payload, instance.ID, err)
	}
	return nil
}

func (s *Service) UserDeleted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.DeletedObjectResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.UserDeleted,
		Payload:   payload,
		UserID:    &payload.ID,
	})
}

func (s *Service) UserUpdated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.UserResponse) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.UserUpdated,
		Payload:   payload,
		UserID:    &payload.ID,
	})
}

func (s *Service) PermissionCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.PermissionResponse,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.PermissionCreated,
		Payload:   payload,
	})
}

func (s *Service) PermissionUpdated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.PermissionResponse,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.PermissionUpdated,
		Payload:   payload,
	})
}

func (s *Service) PermissionDeleted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.DeletedObjectResponse,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.PermissionDeleted,
		Payload:   payload,
	})
}

func (s *Service) RoleCreated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.RoleResponse,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.RoleCreated,
		Payload:   payload,
	})
}

func (s *Service) RoleUpdated(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.RoleResponse,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.RoleUpdated,
		Payload:   payload,
	})
}

func (s *Service) RoleDeleted(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	payload *serialize.DeletedObjectResponse,
) error {
	return s.sendEvent(ctx, exec, sendEventParams{
		Instance:  instance,
		EventType: events.EventTypes.RoleDeleted,
		Payload:   payload,
	})
}
