package events

import (
	"time"

	"github.com/google/uuid"
)

// Topics published by swiftbon.identity.service.
const (
	TopicOrganisationCreated = "organisation-created"
	TopicMemberInvited       = "member-invited"
	TopicMemberJoined        = "member-joined"
	TopicMemberRoleChanged   = "member-role-changed"
	TopicMemberRemoved       = "member-removed"
	TopicSessionRevoked      = "session-revoked"
	TopicMFAResetRequested   = "mfa-reset-requested"
)

// Schema versions of the identity payloads.
const (
	SchemaVersionOrganisationCreated int32 = 1
	SchemaVersionMemberInvited       int32 = 1
	SchemaVersionMemberJoined        int32 = 1
	SchemaVersionMemberRoleChanged   int32 = 1
	SchemaVersionMemberRemoved       int32 = 1
	SchemaVersionSessionRevoked      int32 = 1
	SchemaVersionMFAResetRequested   int32 = 1
)

// OrganisationCreated: a user verified their email and an organisation was made.
type OrganisationCreated struct {
	OrgID             uuid.UUID `json:"org_id"`
	Kind              string    `json:"kind"` // business | personal
	DisplayName       string    `json:"display_name"`
	OwnerUserID       uuid.UUID `json:"owner_user_id"`
	HomeCountry       string    `json:"home_country"`
	ReportingCurrency string    `json:"reporting_currency"`
	CreatedAt         time.Time `json:"created_at"`
}

// MemberInvited: an invitation was issued, which happens once its approval completes.
type MemberInvited struct {
	OrgID             uuid.UUID  `json:"org_id"`
	InvitationID      uuid.UUID  `json:"invitation_id"`
	Email             string     `json:"email"`
	Role              string     `json:"role"`
	InvitedBy         uuid.UUID  `json:"invited_by"`
	ApprovalRequestID *uuid.UUID `json:"approval_request_id,omitempty"`
	ExpiresAt         time.Time  `json:"expires_at"`
}

// MemberJoined: an invitee accepted and became a member.
type MemberJoined struct {
	OrgID    uuid.UUID `json:"org_id"`
	UserID   uuid.UUID `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// MemberRoleChanged: a member's role changed after approval.
type MemberRoleChanged struct {
	OrgID             uuid.UUID  `json:"org_id"`
	UserID            uuid.UUID  `json:"user_id"`
	OldRole           string     `json:"old_role"`
	NewRole           string     `json:"new_role"`
	ChangedBy         uuid.UUID  `json:"changed_by"`
	ApprovalRequestID *uuid.UUID `json:"approval_request_id,omitempty"`
	ChangedAt         time.Time  `json:"changed_at"`
}

// MemberRemoved: a member was deactivated. Removal is immediate and not approved.
type MemberRemoved struct {
	OrgID     uuid.UUID `json:"org_id"`
	UserID    uuid.UUID `json:"user_id"`
	RemovedBy uuid.UUID `json:"removed_by"`
	Reason    string    `json:"reason,omitempty"`
	RemovedAt time.Time `json:"removed_at"`
}

// SessionRevoked: a session ended early. The gateway drops it from its cache.
type SessionRevoked struct {
	OrgID     uuid.UUID `json:"org_id"`
	UserID    uuid.UUID `json:"user_id"`
	SessionID string    `json:"session_id"`
	Reason    string    `json:"reason"` // logout | member_removed | mfa_reset | admin | expired
	RevokedAt time.Time `json:"revoked_at"`
}

// MFAResetRequested: a user opened the lost-authenticator flow.
type MFAResetRequested struct {
	OrgID   uuid.UUID `json:"org_id"`
	UserID  uuid.UUID `json:"user_id"`
	ResetID uuid.UUID `json:"reset_id"`
	// RequiresMemberConfirmation is true for owners with another active member.
	RequiresMemberConfirmation bool `json:"requires_member_confirmation"`
	// ParkedForSupport is true when the user is the organisation's only member.
	ParkedForSupport bool      `json:"parked_for_support"`
	RequestedAt      time.Time `json:"requested_at"`
}

func (OrganisationCreated) TopicName() string     { return TopicOrganisationCreated }
func (OrganisationCreated) PayloadVersion() int32 { return SchemaVersionOrganisationCreated }
func (MemberInvited) TopicName() string           { return TopicMemberInvited }
func (MemberInvited) PayloadVersion() int32       { return SchemaVersionMemberInvited }
func (MemberJoined) TopicName() string            { return TopicMemberJoined }
func (MemberJoined) PayloadVersion() int32        { return SchemaVersionMemberJoined }
func (MemberRoleChanged) TopicName() string       { return TopicMemberRoleChanged }
func (MemberRoleChanged) PayloadVersion() int32   { return SchemaVersionMemberRoleChanged }
func (MemberRemoved) TopicName() string           { return TopicMemberRemoved }
func (MemberRemoved) PayloadVersion() int32       { return SchemaVersionMemberRemoved }
func (SessionRevoked) TopicName() string          { return TopicSessionRevoked }
func (SessionRevoked) PayloadVersion() int32      { return SchemaVersionSessionRevoked }
func (MFAResetRequested) TopicName() string       { return TopicMFAResetRequested }
func (MFAResetRequested) PayloadVersion() int32   { return SchemaVersionMFAResetRequested }
