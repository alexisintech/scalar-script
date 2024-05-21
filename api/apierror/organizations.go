package apierror

import (
	"fmt"
	"net/http"
	"strings"
)

func NotAnAdminInOrganization(userIDs ...string) Error {
	who := "Current user"
	if len(userIDs) > 0 {
		who = strings.Join(userIDs, ", ")
	}
	return New(http.StatusForbidden, &mainError{
		shortMessage: "not an administrator",
		longMessage:  fmt.Sprintf("%s is not an administrator in the organization. Only administrators can perform this action.", who),
		code:         NotAnAdminInOrganizationCode,
	})
}

// 403 - Only for organization members
func NotAMemberInOrganization() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "not a member",
		longMessage:  "Current user is not a member of the organization. Only organization members can perform this action.",
		code:         NotAMemberInOrganizationCode,
	})
}

// 400 - User with given id is already a member of the
// organization and cannot be added again
func AlreadyAMemberOfOrganization(userID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "already a member",
		longMessage:  fmt.Sprintf("User %s is already a member of the organization.", userID),
		code:         AlreadyAMemberInOrganizationCode,
	})
}

func OrganizationMinimumPermissionsNeeded() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "minimum organization permissions needed",
		longMessage:  "There has to be at least one organization member with the minimum required permissions",
		code:         OrganizationMinimumPermissionsNeededCode,
	})
}

// 404 - Invitation is not pending.
func OrganizationInvitationNotPending() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not pending",
		longMessage:  `The organization invitation is not in the "pending" status.`,
		code:         OrganizationInvitationNotPendingCode,
	})
}

// 404 - Invitation not found.
func OrganizationInvitationNotFound(invitationID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  fmt.Sprintf("No invitation found with id %s.", invitationID),
		code:         OrganizationInvitationNotFoundCode,
	})
}

// 400 - Creator doesn't exist
func OrganizationCreatorNotFound(userID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "creator not found",
		longMessage:  fmt.Sprintf("No users found with id %s.", userID),
		code:         OrganizationCreatorNotFoundCode,
	})
}

func OrganizationInvitationRevoked() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invitation has been revoked",
		longMessage:  "This invitation has been revoked and cannot be used anymore.",
		code:         OrganizationInvitationRevokedCode,
	})
}

func OrganizationInvitationAlreadyAccepted() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invitation has already been accepted",
		longMessage:  "This invitation has already been accepted. Sign in instead.",
		code:         OrganizationInvitationAlreadyAcceptedCode,
	})
}

func OrganizationInvitationIdentificationNotExist() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "identification not found",
		longMessage:  "This invitation refers to a non-existing identification.",
		code:         OrganizationInvitationIdentificationNotExistCode,
	})
}

func OrganizationInvitationIdentificationAlreadyExists() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "email address already exists",
		longMessage:  "The email address in this invitation already exists. If it belongs to you, try signing in instead.",
		code:         OrganizationInvitationIdentificationAlreadyExistsCode,
	})
}

func OrganizationInvitationNotUnique() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "organization invitation not unique",
		longMessage:  "Organizations cannot have duplicate pending invitations for an email address.",
		code:         OrganizationInvitationNotUniqueCode,
	})
}

func OrganizationSuggestionAlreadyAccepted() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "suggestion has already been accepted",
		longMessage:  "This organization suggestion has already been accepted.",
		code:         OrganizationSuggestionAlreadyAcceptedCode,
	})
}

func OrganizationNotEnabledInInstance() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "access denied",
		longMessage:  "The organizations feature is not enabled for this instance. You can enable it at https://dashboard.clerk.com.",
		code:         OrganizationNotEnabledInInstanceCode,
	})
}

func OrganizationNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  "Given organization not found.",
		code:         ResourceNotFoundCode,
	})
}

func OrganizationQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization quota exceeded",
		longMessage:  fmt.Sprintf("You have reached your limit of %d organizations. You can remove the organization limit by upgrading to a paid plan or using a production instance.", maxAllowed),
		code:         OrganizationQuotaExceededCode,
	})
}

func OrganizationMembershipQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization membership quota exceeded",
		longMessage:  fmt.Sprintf("You have reached your limit of %d organization memberships, including outstanding invitations.", maxAllowed),
		code:         OrganizationMembershipQuotaExceededCode,
	})
}

func OrganizationMembershipPlanQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization membership quota exceeded",
		longMessage: fmt.Sprintf("You have reached the limit of %d organization memberships allowed by the subscription plan. Please upgrade your subscription to add more.",
			maxAllowed),
		code: OrganizationMembershipPlanQuotaExceededCode,
	})
}

func OrganizationAdminDeleteNotEnabled() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "admin delete not enabled",
		longMessage:  "Deletion by admin is not enabled for this organization.",
		code:         OrganizationAdminDeleteNotEnabledCode,
	})
}

func OrganizationInvitationToDeletedOrganization() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "organization invitation to deleted organization",
		longMessage:  "This invitation refers to an organization that has been deleted.",
		code:         OrganizationInvitationToDeletedOrganizationCode,
	})
}

func OrganizationDomainMismatch(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Organization domain mismatch",
		longMessage:  "The provided email address doesn't match the organization domain name.",
		code:         OrganizationDomainMismatchCode,
		meta:         &formParameter{Name: param},
	})
}

func OrganizationUnlimitedMembershipsRequired() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization has limited memberships",
		longMessage:  "This feature is not supported because organization membership is limited. You can remove the limit by upgrading your subscription plan.",
		code:         OrganizationUnlimitedMembershipsRequiredCode,
	})
}

func OrganizationDomainCommon(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "common email domain",
		longMessage:  "This is a common email provider domain. Please use a different one.",
		code:         OrganizationDomainCommonCode,
		meta:         &formParameter{Name: param},
	})
}

func OrganizationDomainBlocked(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "blocked email domain",
		longMessage:  "This is a blocked email provider domain. Please use a different one.",
		code:         OrganizationDomainBlockedCode,
		meta:         &formParameter{Name: param},
	})
}

func OrganizationDomainAlreadyExists(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "organizaton domain already exists",
		longMessage:  "This domain is already used by another organization.",
		code:         OrganizationDomainAlreadyExistsCode,
		meta:         &formParameter{Name: param},
	})
}

func OrganizationDomainsNotEnabled() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization domains not enabled",
		longMessage:  "This instance does not have domains enabled for organizations.",
		code:         OrganizationDomainsNotEnabledCode,
	})
}

func OrganizationDomainEnrollmentModeNotEnabled(enrollmentMode string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization enrollment mode not enabled",
		longMessage:  fmt.Sprintf("Enrollment mode %s is not enabled for this instances's organizations.", enrollmentMode),
		code:         OrganizationDomainEnrollmentModeNotEnabledCode,
	})
}

func OrganizationDomainQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization domains quota exceeded",
		longMessage:  fmt.Sprintf("You have reached your limit of %d domains per organization.", maxAllowed),
		code:         OrganizationDomainQuotaExceededCode,
	})
}

func MissingOrganizationPermission(permissions ...string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "missing permission",
		longMessage:  "Current user is missing an organization permission.",
		code:         MissingOrganizationPermissionCode,
		meta:         &missingPermissions{Permissions: permissions},
	})
}

func OrganizationRoleUsedAsCreatorRole() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "role is used as the creator role",
		longMessage:  "The organization role cannot be deleted as it is currently used as the creator role.",
		code:         OrganizationRoleUsedAsDefaultCreatorRoleCode,
	})
}

func OrganizationRoleUsedAsDomainDefaultRole() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "role is used as the domain default role",
		longMessage:  "The organization role cannot be deleted as it is currently used as the default domain role.",
		code:         OrganizationRoleUsedAsDomainDefaultRoleCode,
	})
}

func OrganizationRoleAssignedToMembers() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "role is assigned to organization members",
		longMessage:  "The organization role is currently assigned to one or more organization members.",
		code:         OrganizationRoleAssignedToMembersCode,
	})
}

func OrganizationRoleExistsInInvitations() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "role exists in pending organization invitations",
		longMessage:  "The organization role exists in one or more pending organization invitations. Please revoke these invitations to proceed.",
		code:         OrganizationRoleExistsInInvitationsCode,
	})
}

func OrganizationPermissionNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  "Organization permission not found",
		code:         ResourceNotFoundCode,
	})
}

func OrganizationRoleNotFound(paramName string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  "Organization role not found",
		code:         ResourceNotFoundCode,
		meta:         &formParameter{Name: paramName},
	})
}

func OrganizationMissingCreatorRolePermissions(permKeys ...string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "missing permissions for creator role",
		longMessage:  fmt.Sprintf("The creator role must contain the following permissions: %s", strings.Join(permKeys, ", ")),
		code:         OrganizationMissingCreatorRolePermissionsCode,
	})
}

func OrganizationSystemPermissionNotModifiable() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization system permission cannot be modified",
		longMessage:  "This organization permission cannot be modified because it is a system permission.",
		code:         OrganizationSystemPermissionNotModifiableCode,
	})
}

func OrganizationRolePermissionAssociationExists() Error {
	return New(http.StatusConflict, &mainError{
		shortMessage: "permission already assigned to role",
		longMessage:  "This organization permission is already associated to this organization role.",
		code:         OrganizationRolePermissionAssociationExistsCode,
	})
}

func OrganizationRolePermissionAssociationNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "permission not assigned to role",
		longMessage:  "This organization permission is not associated with the organization role.",
		code:         OrganizationRolePermissionAssociationNotFoundCode,
	})
}

func OrganizationInstanceRolesQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "organization roles for instance quota exceeded",
		longMessage:  fmt.Sprintf("You have reached your limit of %d organization roles per instance.", maxAllowed),
		code:         OrganizationInstanceRolesQuotaExceededCode,
	})
}

func OrganizationInstancePermissionsQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "custom organization permissions for instance quota exceeded",
		longMessage:  fmt.Sprintf("You have reached your limit of %d organization permissions per instance.", maxAllowed),
		code:         OrganizationInstancePermissionsQuotaExceededCode,
	})
}
