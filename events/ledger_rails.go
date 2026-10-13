package events

import (
	"time"

	"github.com/google/uuid"

	"swiftbon.com/shared/money"
)

// Topics published by swiftbon.postings.service and the rail adapters
// (fcy, paylony, juice).
const (
	TopicHoldPlaced      = "hold-placed"
	TopicHoldReleased    = "hold-released"
	TopicHoldCaptured    = "hold-captured"
	TopicInboundCredited = "inbound-credited"

	TopicProviderSubaccountStatusChanged = "provider-subaccount-status-changed"
	TopicProviderRFIOpened               = "provider-rfi-opened"
	TopicProviderCapabilityChanged       = "provider-capability-changed"
	TopicInboundPaymentReceived          = "inbound-payment-received"
	TopicInboundPaymentPending           = "inbound-payment-pending"
	TopicPayoutStatusChanged             = "payout-status-changed"
	TopicRailObservation                 = "rail-observation"
)

// Schema versions of the postings and rail payloads.
const (
	SchemaVersionHoldPlaced      int32 = 1
	SchemaVersionHoldReleased    int32 = 1
	SchemaVersionHoldCaptured    int32 = 1
	SchemaVersionInboundCredited int32 = 1

	SchemaVersionProviderSubaccountStatusChanged int32 = 1
	SchemaVersionProviderRFIOpened               int32 = 1
	SchemaVersionProviderCapabilityChanged       int32 = 1
	SchemaVersionInboundPaymentReceived          int32 = 1
	SchemaVersionInboundPaymentPending           int32 = 1
	SchemaVersionPayoutStatusChanged             int32 = 1
	SchemaVersionRailObservation                 int32 = 1
)

// HoldPlaced: funds were reserved on an account.
type HoldPlaced struct {
	OrgID         uuid.UUID   `json:"org_id"`
	HoldID        uuid.UUID   `json:"hold_id"`
	AccountNumber string      `json:"account_number"`
	Amount        money.Money `json:"amount"`
	Reason        string      `json:"reason"` // payout | inbound_screening | ...
	Owner         string      `json:"owner"`  // the service that must release or capture it
	Reference     string      `json:"reference"`
	PlacedAt      time.Time   `json:"placed_at"`
}

// HoldReleased: a hold ended without a debit.
type HoldReleased struct {
	OrgID         uuid.UUID   `json:"org_id"`
	HoldID        uuid.UUID   `json:"hold_id"`
	AccountNumber string      `json:"account_number"`
	Amount        money.Money `json:"amount"`
	Reference     string      `json:"reference"`
	ReleasedAt    time.Time   `json:"released_at"`
}

// HoldCaptured: a hold was consumed by a posting.
type HoldCaptured struct {
	OrgID            uuid.UUID   `json:"org_id"`
	HoldID           uuid.UUID   `json:"hold_id"`
	AccountNumber    string      `json:"account_number"`
	Amount           money.Money `json:"amount"`
	PostingReference string      `json:"posting_reference"`
	CapturedAt       time.Time   `json:"captured_at"`
}

// InboundCredited: an inbound payment was credited. HoldID is set when the
// credit and a screening hold committed together.
type InboundCredited struct {
	OrgID             uuid.UUID   `json:"org_id"`
	AccountNumber     string      `json:"account_number"`
	Amount            money.Money `json:"amount"`
	PostingReference  string      `json:"posting_reference"`
	HoldID            *uuid.UUID  `json:"hold_id,omitempty"`
	Provider          string      `json:"provider"`
	ProviderReference string      `json:"provider_reference"`
	CreditedAt        time.Time   `json:"credited_at"`
}

// ProviderSubaccountStatusChanged: a provider-side business sub-account changed status.
type ProviderSubaccountStatusChanged struct {
	OrgID          uuid.UUID `json:"org_id"`
	Provider       string    `json:"provider"`
	SubAccountID   string    `json:"sub_account_id"`
	Status         string    `json:"status"`
	PreviousStatus string    `json:"previous_status"`
	ChangedAt      time.Time `json:"changed_at"`
}

// ProviderRFIOpened: a provider asked for more information about a business.
type ProviderRFIOpened struct {
	OrgID             uuid.UUID       `json:"org_id"`
	Provider          string          `json:"provider"`
	ProviderReference string          `json:"provider_reference"`
	Title             string          `json:"title"`
	Body              string          `json:"body"`
	DueAt             time.Time       `json:"due_at"`
	Blocks            []CapabilityRef `json:"blocks"`
	OpenedAt          time.Time       `json:"opened_at"`
}

// ProviderCapabilityChanged: a provider enabled or blocked a capability. This
// and ops actions are the only way capability state changes; nothing infers it.
type ProviderCapabilityChanged struct {
	OrgID                uuid.UUID `json:"org_id"`
	Provider             string    `json:"provider"`
	Capability           string    `json:"capability"`
	Currency             string    `json:"currency"`
	State                string    `json:"state"` // enabled | blocked_rfi | not_enabled | pending
	ProviderRFIReference string    `json:"provider_rfi_reference,omitempty"`
	ChangedAt            time.Time `json:"changed_at"`
}

// InboundPaymentReceived: a provider reported a settled inbound payment.
type InboundPaymentReceived struct {
	OrgID             uuid.UUID   `json:"org_id"`
	Provider          string      `json:"provider"`
	ProviderReference string      `json:"provider_reference"`
	AccountNumber     string      `json:"account_number"`
	Amount            money.Money `json:"amount"`
	Rail              string      `json:"rail"`
	PayerName         string      `json:"payer_name"`
	PayerAccount      string      `json:"payer_account,omitempty"`
	PayerBank         string      `json:"payer_bank,omitempty"`
	Narration         string      `json:"narration,omitempty"`
	ReceivedAt        time.Time   `json:"received_at"`
}

// InboundPaymentPending: a provider announced an inbound payment that has not
// settled. It is not a ledger fact; it feeds the wallet's pending_in figure.
type InboundPaymentPending struct {
	OrgID             uuid.UUID   `json:"org_id"`
	Provider          string      `json:"provider"`
	ProviderReference string      `json:"provider_reference"`
	AccountNumber     string      `json:"account_number"`
	Amount            money.Money `json:"amount"`
	Rail              string      `json:"rail"`
	PayerName         string      `json:"payer_name,omitempty"`
	AnnouncedAt       time.Time   `json:"announced_at"`
}

// PayoutStatusChanged: a provider reported a new state for a payout attempt.
type PayoutStatusChanged struct {
	OrgID             uuid.UUID `json:"org_id"`
	Provider          string    `json:"provider"`
	PayoutReference   string    `json:"payout_reference"` // PO-#####
	AttemptKey        string    `json:"attempt_key"`      // PO-#####-n
	State             string    `json:"state"`            // ACCEPTED | PROCESSING | SETTLED | RETURNED | FAILED | UNKNOWN | NOT_FOUND
	ReturnCode        string    `json:"return_code,omitempty"`
	ReturnReason      string    `json:"return_reason,omitempty"`
	ProviderReference string    `json:"provider_reference,omitempty"`
	SafeToReroute     bool      `json:"safe_to_reroute"`
	ChangedAt         time.Time `json:"changed_at"`
}

// RailObservation: one measured outcome from a rail, which treasury aggregates
// into rail health. It is about the platform, so OrgID is uuid.Nil.
type RailObservation struct {
	OrgID      uuid.UUID `json:"org_id"`
	Provider   string    `json:"provider"`
	Rail       string    `json:"rail"`
	Currency   string    `json:"currency"`
	Operation  string    `json:"operation"` // submit | status | name_enquiry
	Success    bool      `json:"success"`
	LatencyMs  int64     `json:"latency_ms"`
	ErrorClass string    `json:"error_class,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

func (HoldPlaced) TopicName() string          { return TopicHoldPlaced }
func (HoldPlaced) PayloadVersion() int32      { return SchemaVersionHoldPlaced }
func (HoldReleased) TopicName() string        { return TopicHoldReleased }
func (HoldReleased) PayloadVersion() int32    { return SchemaVersionHoldReleased }
func (HoldCaptured) TopicName() string        { return TopicHoldCaptured }
func (HoldCaptured) PayloadVersion() int32    { return SchemaVersionHoldCaptured }
func (InboundCredited) TopicName() string     { return TopicInboundCredited }
func (InboundCredited) PayloadVersion() int32 { return SchemaVersionInboundCredited }
func (ProviderSubaccountStatusChanged) TopicName() string {
	return TopicProviderSubaccountStatusChanged
}
func (ProviderSubaccountStatusChanged) PayloadVersion() int32 {
	return SchemaVersionProviderSubaccountStatusChanged
}
func (ProviderRFIOpened) TopicName() string         { return TopicProviderRFIOpened }
func (ProviderRFIOpened) PayloadVersion() int32     { return SchemaVersionProviderRFIOpened }
func (ProviderCapabilityChanged) TopicName() string { return TopicProviderCapabilityChanged }
func (ProviderCapabilityChanged) PayloadVersion() int32 {
	return SchemaVersionProviderCapabilityChanged
}
func (InboundPaymentReceived) TopicName() string     { return TopicInboundPaymentReceived }
func (InboundPaymentReceived) PayloadVersion() int32 { return SchemaVersionInboundPaymentReceived }
func (InboundPaymentPending) TopicName() string      { return TopicInboundPaymentPending }
func (InboundPaymentPending) PayloadVersion() int32  { return SchemaVersionInboundPaymentPending }
func (PayoutStatusChanged) TopicName() string        { return TopicPayoutStatusChanged }
func (PayoutStatusChanged) PayloadVersion() int32    { return SchemaVersionPayoutStatusChanged }
func (RailObservation) TopicName() string            { return TopicRailObservation }
func (RailObservation) PayloadVersion() int32        { return SchemaVersionRailObservation }
