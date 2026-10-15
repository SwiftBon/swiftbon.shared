package events

import (
	"time"

	"github.com/google/uuid"
)

// Topics published by swiftbon.documents.service and swiftbon.account.service.
const (
	TopicDocumentChecked        = "document-checked"
	TopicBundleSplit            = "bundle-split"
	TopicDocumentRendered       = "document-rendered"
	TopicBusinessCreated        = "business-created"
	TopicBusinessAccountOpened  = "business-account-opened"
	TopicReceivingDetailsIssued = "receiving-details-issued"
	TopicCapabilityChanged      = "capability-changed"
)

// Schema versions of the documents and account payloads.
const (
	SchemaVersionDocumentChecked        int32 = 1
	SchemaVersionBundleSplit            int32 = 1
	SchemaVersionDocumentRendered       int32 = 1
	SchemaVersionBusinessCreated        int32 = 1
	SchemaVersionBusinessAccountOpened  int32 = 1
	SchemaVersionReceivingDetailsIssued int32 = 1
	SchemaVersionCapabilityChanged      int32 = 1
)

// DocumentChecked: the instant quality check on an uploaded document finished.
type DocumentChecked struct {
	OrgID         uuid.UUID `json:"org_id"`
	DocumentID    uuid.UUID `json:"document_id"`
	RequirementID string    `json:"requirement_id,omitempty"`
	Purpose       string    `json:"purpose"`
	Status        string    `json:"status"` // accepted | rejected
	ReasonCode    string    `json:"reason_code,omitempty"`
	Page          *int      `json:"page,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

// BundleSection is one requirement found inside a combined PDF.
type BundleSection struct {
	RequirementID string `json:"requirement_id"`
	Pages         string `json:"pages"` // "4-6"
}

// BundleSplit: a combined PDF was split into the requirements it contains.
type BundleSplit struct {
	OrgID      uuid.UUID       `json:"org_id"`
	DocumentID uuid.UUID       `json:"document_id"`
	Found      []BundleSection `json:"found"`
	Missing    []string        `json:"missing"`
	SplitAt    time.Time       `json:"split_at"`
}

// DocumentRendered: a generated document (a statement PDF) is ready.
type DocumentRendered struct {
	OrgID       uuid.UUID `json:"org_id"`
	DocumentID  uuid.UUID `json:"document_id"`
	Kind        string    `json:"kind"` // statement_pdf
	SubjectRef  string    `json:"subject_ref"`
	RequestedBy uuid.UUID `json:"requested_by"`
	RenderedAt  time.Time `json:"rendered_at"`
}

// BusinessCreated: the business legal entity was recorded.
type BusinessCreated struct {
	OrgID              uuid.UUID `json:"org_id"`
	BusinessID         uuid.UUID `json:"business_id"`
	LegalName          string    `json:"legal_name"`
	EntityType         string    `json:"entity_type"`
	RegistrationNumber string    `json:"registration_number"`
	Country            string    `json:"country"`
	CreatedAt          time.Time `json:"created_at"`
}

// BusinessAccountOpened: an account in one currency was opened for the business.
type BusinessAccountOpened struct {
	OrgID         uuid.UUID `json:"org_id"`
	AccountNumber string    `json:"account_number"`
	Currency      string    `json:"currency"`
	Provider      string    `json:"provider"`
	State         string    `json:"state"` // active | restricted_outbound | pending
	OpenedAt      time.Time `json:"opened_at"`
}

// ReceivingDetailsIssued: a new version of the account's receiving details exists.
type ReceivingDetailsIssued struct {
	OrgID             uuid.UUID  `json:"org_id"`
	AccountNumber     string     `json:"account_number"`
	Currency          string     `json:"currency"`
	Version           int        `json:"version"`
	SupersedesVersion *int       `json:"supersedes_version,omitempty"`
	Reason            string     `json:"reason"`
	GraceUntil        *time.Time `json:"grace_until,omitempty"`
	IssuedAt          time.Time  `json:"issued_at"`
}

// CapabilityChanged: a capability in one currency changed state.
type CapabilityChanged struct {
	OrgID         uuid.UUID  `json:"org_id"`
	Capability    string     `json:"capability"` // collections | fx_conversion | payouts
	Currency      string     `json:"currency"`
	State         string     `json:"state"` // enabled | blocked_rfi | not_enabled | pending
	PreviousState string     `json:"previous_state"`
	RFIID         *uuid.UUID `json:"rfi_id,omitempty"`
	Source        string     `json:"source"` // provider | ops
	ChangedAt     time.Time  `json:"changed_at"`
}

func (DocumentChecked) TopicName() string            { return TopicDocumentChecked }
func (DocumentChecked) PayloadVersion() int32        { return SchemaVersionDocumentChecked }
func (BundleSplit) TopicName() string                { return TopicBundleSplit }
func (BundleSplit) PayloadVersion() int32            { return SchemaVersionBundleSplit }
func (DocumentRendered) TopicName() string           { return TopicDocumentRendered }
func (DocumentRendered) PayloadVersion() int32       { return SchemaVersionDocumentRendered }
func (BusinessCreated) TopicName() string            { return TopicBusinessCreated }
func (BusinessCreated) PayloadVersion() int32        { return SchemaVersionBusinessCreated }
func (BusinessAccountOpened) TopicName() string      { return TopicBusinessAccountOpened }
func (BusinessAccountOpened) PayloadVersion() int32  { return SchemaVersionBusinessAccountOpened }
func (ReceivingDetailsIssued) TopicName() string     { return TopicReceivingDetailsIssued }
func (ReceivingDetailsIssued) PayloadVersion() int32 { return SchemaVersionReceivingDetailsIssued }
func (CapabilityChanged) TopicName() string          { return TopicCapabilityChanged }
func (CapabilityChanged) PayloadVersion() int32      { return SchemaVersionCapabilityChanged }
