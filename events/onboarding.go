package events

import (
	"time"

	"github.com/google/uuid"
)

// Topics published by swiftbon.onboarding.service.
const (
	TopicApplicationSubmitted = "application-submitted"
	TopicKYBReviewRequested   = "kyb-review-requested"
	TopicApplicationApproved  = "application-approved"
	TopicApplicationDeclined  = "application-declined"
	TopicRFIOpened            = "rfi-opened"
	TopicRFIResponded         = "rfi-responded"
	TopicRFIOverdue           = "rfi-overdue"
)

// Schema versions of the onboarding payloads.
const (
	SchemaVersionApplicationSubmitted int32 = 1
	SchemaVersionKYBReviewRequested   int32 = 1
	SchemaVersionApplicationApproved  int32 = 1
	SchemaVersionApplicationDeclined  int32 = 1
	SchemaVersionRFIOpened            int32 = 1
	SchemaVersionRFIResponded         int32 = 1
	SchemaVersionRFIOverdue           int32 = 1
)

// CapabilityRef names one capability in one currency, as an RFI blocks it.
type CapabilityRef struct {
	Capability string `json:"capability"` // collections | fx_conversion | payouts
	Currency   string `json:"currency"`
}

// ApplicationSubmitted: the applicant submitted the KYB application. Swiftbon
// KYB and provider sub-account onboarding start in parallel.
type ApplicationSubmitted struct {
	OrgID         uuid.UUID `json:"org_id"`
	ApplicationID uuid.UUID `json:"application_id"`
	EntityType    string    `json:"entity_type"` // ltd | plc | bn | it
	LegalName     string    `json:"legal_name"`
	Currencies    []string  `json:"currencies"`
	SubmittedBy   uuid.UUID `json:"submitted_by"`
	SubmittedAt   time.Time `json:"submitted_at"`
}

// KYBReviewRequested: a compliance KYB case should be opened for the application.
type KYBReviewRequested struct {
	OrgID         uuid.UUID `json:"org_id"`
	ApplicationID uuid.UUID `json:"application_id"`
	LegalName     string    `json:"legal_name"`
	// ManualRegistry is true when the applicant verified with a certificate
	// instead of a registry match, which adds manual review time.
	ManualRegistry bool      `json:"manual_registry"`
	RequestedAt    time.Time `json:"requested_at"`
}

// ApplicationApproved: Swiftbon KYB was approved after the maker-checker disposition.
type ApplicationApproved struct {
	OrgID         uuid.UUID `json:"org_id"`
	ApplicationID uuid.UUID `json:"application_id"`
	DecisionRef   string    `json:"decision_ref"` // DEC-####
	RiskRating    string    `json:"risk_rating"`  // low | medium | high
	ApprovedAt    time.Time `json:"approved_at"`
}

// ApplicationDeclined: Swiftbon KYB was declined.
type ApplicationDeclined struct {
	OrgID          uuid.UUID `json:"org_id"`
	ApplicationID  uuid.UUID `json:"application_id"`
	DecisionRef    string    `json:"decision_ref"` // DEC-####
	Reason         string    `json:"reason"`
	AppealDeadline time.Time `json:"appeal_deadline"`
	ReapplyAfter   time.Time `json:"reapply_after"`
	DeclinedAt     time.Time `json:"declined_at"`
}

// RFIOpened: Swiftbon or a provider asked the applicant for more information.
type RFIOpened struct {
	OrgID       uuid.UUID       `json:"org_id"`
	RFIID       uuid.UUID       `json:"rfi_id"`
	Reference   string          `json:"reference"`    // RFI-####
	RequestedBy string          `json:"requested_by"` // swiftbon | provider
	Provider    string          `json:"provider,omitempty"`
	Title       string          `json:"title"`
	DueAt       time.Time       `json:"due_at"`
	Blocks      []CapabilityRef `json:"blocks"`
	OpenedAt    time.Time       `json:"opened_at"`
}

// RFIResponded: the applicant answered an RFI.
type RFIResponded struct {
	OrgID       uuid.UUID   `json:"org_id"`
	RFIID       uuid.UUID   `json:"rfi_id"`
	Reference   string      `json:"reference"`
	DocumentIDs []uuid.UUID `json:"document_ids"`
	RespondedBy uuid.UUID   `json:"responded_by"`
	RespondedAt time.Time   `json:"responded_at"`
}

// RFIOverdue: an RFI passed its due date unanswered. Activation of the
// capabilities it blocks pauses, and a compliance case opens.
type RFIOverdue struct {
	OrgID      uuid.UUID       `json:"org_id"`
	RFIID      uuid.UUID       `json:"rfi_id"`
	Reference  string          `json:"reference"`
	DueAt      time.Time       `json:"due_at"`
	Blocks     []CapabilityRef `json:"blocks"`
	DetectedAt time.Time       `json:"detected_at"`
}

func (ApplicationSubmitted) TopicName() string     { return TopicApplicationSubmitted }
func (ApplicationSubmitted) PayloadVersion() int32 { return SchemaVersionApplicationSubmitted }
func (KYBReviewRequested) TopicName() string       { return TopicKYBReviewRequested }
func (KYBReviewRequested) PayloadVersion() int32   { return SchemaVersionKYBReviewRequested }
func (ApplicationApproved) TopicName() string      { return TopicApplicationApproved }
func (ApplicationApproved) PayloadVersion() int32  { return SchemaVersionApplicationApproved }
func (ApplicationDeclined) TopicName() string      { return TopicApplicationDeclined }
func (ApplicationDeclined) PayloadVersion() int32  { return SchemaVersionApplicationDeclined }
func (RFIOpened) TopicName() string                { return TopicRFIOpened }
func (RFIOpened) PayloadVersion() int32            { return SchemaVersionRFIOpened }
func (RFIResponded) TopicName() string             { return TopicRFIResponded }
func (RFIResponded) PayloadVersion() int32         { return SchemaVersionRFIResponded }
func (RFIOverdue) TopicName() string               { return TopicRFIOverdue }
func (RFIOverdue) PayloadVersion() int32           { return SchemaVersionRFIOverdue }
