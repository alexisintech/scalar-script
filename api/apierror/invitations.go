package apierror

import (
	"fmt"
	"net/http"
	"strings"
)

// InvitationsNotSupportedInInstance denotes an error when user is
// trying to create an invitation on an instance that doesn't support it
func InvitationsNotSupportedInInstance() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invitations are only supported on instances that accept email addresses.",
		code:         InvitationsNotSupportedInInstanceCode,
	})
}

// DuplicateInvitations denotes an error when there are already invitations
// for the given email addresses
func DuplicateInvitations(emailAddresses ...string) Error {
	shortMessage := "duplicate invitation"
	if len(emailAddresses) > 1 {
		shortMessage += "s"
	}
	return New(http.StatusBadRequest, &mainError{
		shortMessage: shortMessage,
		longMessage: fmt.Sprintf("There are already pending invitations for the following email addresses: %s",
			strings.Join(emailAddresses, ", ")),
		code: DuplicateRecordCode,
		meta: &duplicateInvitationEmails{
			EmailAddresses: emailAddresses,
		},
	})
}

// RevokedInvitation denotes an error when the given invitation token
// does not correspond to any invitations, which means that the invitation
// has been removed.
func RevokedInvitation() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "The invitation was revoked.",
		code:         RevokedInvitationCode,
	})
}

// InvitationAlreadyAccepted denotes an error when someone tries to use
// an invitation which is already accepted.
func InvitationAlreadyAccepted() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invitation is already accepted, try signing in instead.",
		code:         InvitationAlreadyAcceptedCode,
	})
}

// InvitationAlreadyRevoked denotes an error when someone tries to revoke
// an invitation which is already revoked.
func InvitationAlreadyRevoked() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invitation is already revoked.",
		code:         InvitationAlreadyRevokedCode,
	})
}

// InvitationNotFound denotes an error when there is no invitation with
// the given id
func InvitationNotFound(invitationID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  fmt.Sprintf("No invitation was found with id %s.", invitationID),
		code:         ResourceNotFoundCode,
	})
}

// InvitationAccountAlreadyExists denotes an error when there is an existing
// user identification with the same email as the invitation.
func InvitationAccountAlreadyExists() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "account exists",
		longMessage:  "An account already exists for this invitation. Sign in instead.",
		code:         InvitationAccountAlreadyExistsCode,
	})
}

func InvitationIdentificationNotExist() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "identification not found",
		longMessage:  "This invitation refers to a non-existing identification.",
		code:         InvitationIdentificationNotExistCode,
	})
}
