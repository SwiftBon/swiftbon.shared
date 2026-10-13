package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"swiftbon.com/shared/money"
)

// Topics published by approvals, compliance, payouts, support, treasury,
// communication, and every service that owns runtime settings.
const (
	TopicApprovalRequested = "approval-requested"
	TopicApprovalDecided   = "approval-decided"
	TopicApprovalExpired   = "approval-expired"

	TopicCaseOpened        = "case-opened"
	TopicCaseDispositioned = "case-dispositioned"
	TopicAuditRecorded     = "audit-recorded"

	TopicBeneficiaryStatusChanged  = "beneficiary-status-changed"
	TopicPayoutStatusChangedPublic = "payout-status-changed-public"

	TopicSupportCaseOpened  = "support-case-opened"
	TopicSupportCaseUpdated = "support-case-updated"

	TopicRailStatusChanged  = "rail-status-changed"
	TopicReconBreakDetected = "recon-break-detected"
	TopicSafeguardingBreach = "safeguarding-breach"

	TopicNotificationRequested = "notification-requested"
	TopicSettingChanged        = "setting-changed"
)

// Schema versions of the workflow payloads.
const (
	SchemaVersionApprovalRequested int32 = 1
	SchemaVersionApprovalDecided   int32 = 1
	SchemaVersionApprovalExpired   int32 = 1

	SchemaVersionCaseOpened        int32 = 1
	SchemaVersionCaseDispositioned int32 = 1
	SchemaVersionAuditRecorded     int32 = 1

	SchemaVersionBeneficiaryStatusChanged  int32 = 1
	SchemaVersionPayoutStatusChangedPublic int32 = 1

	SchemaVersionSupportCaseOpened  int32 = 1
	SchemaVersionSupportCaseUpdated int32 = 1

	SchemaVersionRailStatusChanged  int32 = 1
	SchemaVersionReconBreakDetected int32 = 1
	SchemaVersionSafeguardingBreach int32 = 1

	SchemaVersionNotificationRequested int32 = 1
	SchemaVersionSettingChanged        int32 = 1
)

// ApprovalRequested: a domain service asked approvals to decide something.
type ApprovalRequested struct {
	OrgID             uuid.UUID    `json:"org_id"`
	ApprovalID        uuid.UUID    `json:"approval_id"`
	Reference         string       `json:"reference"` // APR-####
	Kind              string       `json:"kind"`      // payout | beneficiary | member_invite | member_role_change | policy_change | api_key | mfa_reset | setting_change
	SubjectRef        string       `json:"subject_ref"`
	SubjectService    string       `json:"subject_service"`
	Summary           string       `json:"summary"`
	Amount            *money.Money `json:"amount,omitempty"`
	InitiatedBy       uuid.UUID    `json:"initiated_by"`
	ApproversRequired int          `json:"approvers_required"`
	PolicyVersion     int          `json:"policy_version"`
	ExpiresAt         time.Time    `json:"expires_at"`
	RequestedAt       time.Time    `json:"requested_at"`
}

// ApprovalDecided: an approval reached a final decision.
type ApprovalDecided struct {
	OrgID          uuid.UUID   `json:"org_id"`
	ApprovalID     uuid.UUID   `json:"approval_id"`
	Reference      string      `json:"reference"`
	Kind           string      `json:"kind"`
	SubjectRef     string      `json:"subject_ref"`
	SubjectService string      `json:"subject_service"`
	Decision       string      `json:"decision"` // approved | rejected | cancelled
	DecidedBy      []uuid.UUID `json:"decided_by"`
	Reason         string      `json:"reason,omitempty"`
	PolicyVersion  int         `json:"policy_version"`
	DecidedAt      time.Time   `json:"decided_at"`
}

// ApprovalExpired: an approval lapsed without a decision. Holds are released.
type ApprovalExpired struct {
	OrgID          uuid.UUID `json:"org_id"`
	ApprovalID     uuid.UUID `json:"approval_id"`
	Reference      string    `json:"reference"`
	Kind           string    `json:"kind"`
	SubjectRef     string    `json:"subject_ref"`
	SubjectService string    `json:"subject_service"`
	InitiatedBy    uuid.UUID `json:"initiated_by"`
	ExpiredAt      time.Time `json:"expired_at"`
}

// CaseOpened: compliance opened a case.
type CaseOpened struct {
	OrgID      uuid.UUID `json:"org_id"`
	CaseID     uuid.UUID `json:"case_id"`
	Reference  string    `json:"reference"` // CASE-####
	Kind       string    `json:"kind"`      // kyb | screening_hit | inbound_hold | provider_rfi | monitoring
	Severity   string    `json:"severity"`  // high | medium | low
	SubjectRef string    `json:"subject_ref"`
	Summary    string    `json:"summary"`
	SLADueAt   time.Time `json:"sla_due_at"`
	OpenedAt   time.Time `json:"opened_at"`
}

// CaseDispositioned: a maker proposed and a different checker approved a disposition.
type CaseDispositioned struct {
	OrgID       uuid.UUID `json:"org_id"`
	CaseID      uuid.UUID `json:"case_id"`
	Reference   string    `json:"reference"`
	Kind        string    `json:"kind"`
	SubjectRef  string    `json:"subject_ref"`
	Disposition string    `json:"disposition"` // release | return_to_sender | approve | decline | dismiss
	MakerID     uuid.UUID `json:"maker_id"`
	CheckerID   uuid.UUID `json:"checker_id"`
	Reason      string    `json:"reason,omitempty"`
	DecidedAt   time.Time `json:"decided_at"`
}

// AuditActor is who did something.
type AuditActor struct {
	UserID  *uuid.UUID `json:"user_id,omitempty"`
	StaffID *uuid.UUID `json:"staff_id,omitempty"`
	System  string     `json:"system,omitempty"` // the service name, for system actions
	Via     string     `json:"via"`              // session | api_key | impersonation | system
}

// AuditRecorded: a state change worth keeping forever. Every service publishes
// it through its outbox in the same transaction as the change; compliance
// stores it in the append-only audit log.
type AuditRecorded struct {
	OrgID      uuid.UUID       `json:"org_id"`
	Actor      AuditActor      `json:"actor"`
	Action     string          `json:"action"`
	SubjectRef string          `json:"subject_ref"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	RequestID  string          `json:"request_id"`
	IP         string          `json:"ip,omitempty"`
	UserAgent  string          `json:"user_agent,omitempty"`
	At         time.Time       `json:"at"`
}

// BeneficiaryStatusChanged: a beneficiary moved through its lifecycle.
type BeneficiaryStatusChanged struct {
	OrgID           uuid.UUID  `json:"org_id"`
	BeneficiaryID   uuid.UUID  `json:"beneficiary_id"`
	Reference       string     `json:"reference"` // BEN-####
	Status          string     `json:"status"`    // pending_approval | screening | cooling_off | verified | rejected
	PreviousStatus  string     `json:"previous_status"`
	CoolingOffUntil *time.Time `json:"cooling_off_until,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	ChangedAt       time.Time  `json:"changed_at"`
}

// PayoutStatusChangedPublic: a payout changed status, in terms the customer sees.
// It is distinct from the rail-level payout-status-changed.
type PayoutStatusChangedPublic struct {
	OrgID           uuid.UUID   `json:"org_id"`
	PayoutReference string      `json:"payout_reference"` // PO-#####
	Status          string      `json:"status"`
	PreviousStatus  string      `json:"previous_status"`
	Amount          money.Money `json:"amount"`
	Fee             money.Money `json:"fee"`
	Rail            string      `json:"rail"`
	Provider        string      `json:"provider,omitempty"`
	ReturnCode      string      `json:"return_code,omitempty"`
	ReturnReason    string      `json:"return_reason,omitempty"`
	InitiatedBy     uuid.UUID   `json:"initiated_by"`
	ChangedAt       time.Time   `json:"changed_at"`
}

// SupportCaseOpened: a support case was opened.
type SupportCaseOpened struct {
	OrgID     uuid.UUID `json:"org_id"`
	CaseID    uuid.UUID `json:"case_id"`
	Reference string    `json:"reference"` // SUP-####
	Subject   string    `json:"subject"`
	OpenedBy  uuid.UUID `json:"opened_by"`
	Screen    string    `json:"screen,omitempty"`
	LinkedRef string    `json:"linked_ref,omitempty"`
	OpenedAt  time.Time `json:"opened_at"`
}

// SupportCaseUpdated: a support case changed status or received a message.
type SupportCaseUpdated struct {
	OrgID        uuid.UUID `json:"org_id"`
	CaseID       uuid.UUID `json:"case_id"`
	Reference    string    `json:"reference"`
	Status       string    `json:"status"`
	UpdatedBy    uuid.UUID `json:"updated_by"`
	MessageAdded bool      `json:"message_added"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RailStatusChanged: a provider's health on a rail changed. It is about the
// platform, so OrgID is uuid.Nil.
type RailStatusChanged struct {
	OrgID          uuid.UUID `json:"org_id"`
	Currency       string    `json:"currency"`
	Rail           string    `json:"rail"`
	Provider       string    `json:"provider"`
	Status         string    `json:"status"` // operational | degraded | down
	PreviousStatus string    `json:"previous_status"`
	Detail         string    `json:"detail,omitempty"`
	ChangedAt      time.Time `json:"changed_at"`
}

// ReconBreakDetected: the provider's records and the ledger disagree. OrgID is
// uuid.Nil unless the break traces to one organisation.
type ReconBreakDetected struct {
	OrgID          uuid.UUID   `json:"org_id"`
	BreakID        uuid.UUID   `json:"break_id"`
	Provider       string      `json:"provider"`
	Currency       string      `json:"currency"`
	Kind           string      `json:"kind"`
	ProviderAmount money.Money `json:"provider_amount"`
	LedgerAmount   money.Money `json:"ledger_amount"`
	Difference     money.Money `json:"difference"`
	SLADueAt       time.Time   `json:"sla_due_at"`
	DetectedAt     time.Time   `json:"detected_at"`
}

// SafeguardingBreach: customer liabilities exceed safeguarded funds in a
// currency. OrgID is uuid.Nil.
type SafeguardingBreach struct {
	OrgID               uuid.UUID   `json:"org_id"`
	Currency            string      `json:"currency"`
	CustomerLiabilities money.Money `json:"customer_liabilities"`
	SafeguardedFunds    money.Money `json:"safeguarded_funds"`
	Shortfall           money.Money `json:"shortfall"`
	DetectedAt          time.Time   `json:"detected_at"`
}

// Recipient is one address a notification goes to. Exactly one field is set.
type Recipient struct {
	Email  string     `json:"email,omitempty"`
	Phone  string     `json:"phone,omitempty"`
	UserID *uuid.UUID `json:"user_id,omitempty"`
}

// NotificationRequested asks communication to send a templated message.
type NotificationRequested struct {
	OrgID          uuid.UUID         `json:"org_id"`
	Template       string            `json:"template"`
	Locale         string            `json:"locale"`
	To             []Recipient       `json:"to"`
	Vars           map[string]string `json:"vars"`
	IdempotencyKey string            `json:"idempotency_key"`
}

// SettingChanged: a runtime setting got a new version. Replicas of the owning
// service drop their cached snapshot; compliance records it. The value is not
// carried: readers fetch it, and the audit event holds before and after.
type SettingChanged struct {
	// OrgID is the organisation for an org-scoped override, uuid.Nil for a global value.
	OrgID             uuid.UUID  `json:"org_id"`
	Service           string     `json:"service"`
	Key               string     `json:"key"`
	Version           int64      `json:"version"`
	PreviousVersion   int64      `json:"previous_version"`
	ChangedBy         string     `json:"changed_by"`
	ApprovalRequestID *uuid.UUID `json:"approval_request_id,omitempty"`
	Reason            string     `json:"reason"`
	EffectiveFrom     time.Time  `json:"effective_from"`
}

func (ApprovalRequested) TopicName() string            { return TopicApprovalRequested }
func (ApprovalRequested) PayloadVersion() int32        { return SchemaVersionApprovalRequested }
func (ApprovalDecided) TopicName() string              { return TopicApprovalDecided }
func (ApprovalDecided) PayloadVersion() int32          { return SchemaVersionApprovalDecided }
func (ApprovalExpired) TopicName() string              { return TopicApprovalExpired }
func (ApprovalExpired) PayloadVersion() int32          { return SchemaVersionApprovalExpired }
func (CaseOpened) TopicName() string                   { return TopicCaseOpened }
func (CaseOpened) PayloadVersion() int32               { return SchemaVersionCaseOpened }
func (CaseDispositioned) TopicName() string            { return TopicCaseDispositioned }
func (CaseDispositioned) PayloadVersion() int32        { return SchemaVersionCaseDispositioned }
func (AuditRecorded) TopicName() string                { return TopicAuditRecorded }
func (AuditRecorded) PayloadVersion() int32            { return SchemaVersionAuditRecorded }
func (BeneficiaryStatusChanged) TopicName() string     { return TopicBeneficiaryStatusChanged }
func (BeneficiaryStatusChanged) PayloadVersion() int32 { return SchemaVersionBeneficiaryStatusChanged }
func (PayoutStatusChangedPublic) TopicName() string    { return TopicPayoutStatusChangedPublic }
func (PayoutStatusChangedPublic) PayloadVersion() int32 {
	return SchemaVersionPayoutStatusChangedPublic
}
func (SupportCaseOpened) TopicName() string         { return TopicSupportCaseOpened }
func (SupportCaseOpened) PayloadVersion() int32     { return SchemaVersionSupportCaseOpened }
func (SupportCaseUpdated) TopicName() string        { return TopicSupportCaseUpdated }
func (SupportCaseUpdated) PayloadVersion() int32    { return SchemaVersionSupportCaseUpdated }
func (RailStatusChanged) TopicName() string         { return TopicRailStatusChanged }
func (RailStatusChanged) PayloadVersion() int32     { return SchemaVersionRailStatusChanged }
func (ReconBreakDetected) TopicName() string        { return TopicReconBreakDetected }
func (ReconBreakDetected) PayloadVersion() int32    { return SchemaVersionReconBreakDetected }
func (SafeguardingBreach) TopicName() string        { return TopicSafeguardingBreach }
func (SafeguardingBreach) PayloadVersion() int32    { return SchemaVersionSafeguardingBreach }
func (NotificationRequested) TopicName() string     { return TopicNotificationRequested }
func (NotificationRequested) PayloadVersion() int32 { return SchemaVersionNotificationRequested }
func (SettingChanged) TopicName() string            { return TopicSettingChanged }
func (SettingChanged) PayloadVersion() int32        { return SchemaVersionSettingChanged }
