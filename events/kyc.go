package events

import (
	"time"

	"github.com/google/uuid"
)

// Topics published by swiftbon.kyc.service.
const (
	TopicRelatedPartyVerificationCompleted    = "related-party-verification-completed"
	TopicLivenessSessionCompleted             = "liveness-session-completed"
	TopicRegistryLookupCompleted              = "registry-lookup-completed"
	TopicBusinessAddressVerificationCompleted = "business-address-verification-completed"
	// TopicScreeningCompleted and TopicScreeningHitDetected exist today with a
	// person-shaped payload at schema version 1. The versions here are 2: the
	// legacy fields are kept as they are and the subject and organisation are
	// added, so a version-1 consumer still reads a version-2 payload.
	TopicScreeningCompleted   = "screening-completed"
	TopicScreeningHitDetected = "screening-hit-detected"
)

// Schema versions of the kyc payloads.
const (
	SchemaVersionRelatedPartyVerificationCompleted    int32 = 1
	SchemaVersionLivenessSessionCompleted             int32 = 1
	SchemaVersionRegistryLookupCompleted              int32 = 1
	SchemaVersionBusinessAddressVerificationCompleted int32 = 1
	SchemaVersionScreeningCompleted                   int32 = 2
	SchemaVersionScreeningHitDetected                 int32 = 2
)

// RelatedPartyVerificationCompleted: a director, owner or other related party
// finished identity verification.
type RelatedPartyVerificationCompleted struct {
	OrgID         uuid.UUID `json:"org_id"`
	ApplicationID uuid.UUID `json:"application_id"`
	PartyID       uuid.UUID `json:"party_id"`
	// Outcome is verified | records_disagree | liveness_failed | locked.
	Outcome string `json:"outcome"`
	// RecordsAgree is the BVN-to-NIN cross-match result.
	RecordsAgree   bool      `json:"records_agree"`
	NameMatchScore float64   `json:"name_match_score"`
	CompletedAt    time.Time `json:"completed_at"`
}

// LivenessSessionCompleted: a hosted liveness session ended. The biometric
// itself never reaches Swiftbon; this is only the result.
type LivenessSessionCompleted struct {
	OrgID             uuid.UUID `json:"org_id"`
	PartyID           uuid.UUID `json:"party_id"`
	SessionID         uuid.UUID `json:"session_id"`
	Passed            bool      `json:"passed"`
	AttemptsUsed      int       `json:"attempts_used"`
	AttemptsMax       int       `json:"attempts_max"`
	Locked            bool      `json:"locked"`
	ProviderReference string    `json:"provider_reference"`
	CompletedAt       time.Time `json:"completed_at"`
}

// RegistryLookupCompleted: a business registry (CAC) lookup returned.
type RegistryLookupCompleted struct {
	OrgID              uuid.UUID `json:"org_id"`
	ApplicationID      uuid.UUID `json:"application_id"`
	RegistrationNumber string    `json:"registration_number"`
	Status             string    `json:"status"` // matched | no_match | unavailable
	LegalName          string    `json:"legal_name,omitempty"`
	RegisteredOn       string    `json:"registered_on,omitempty"` // YYYY-MM-DD
	RegistryStatus     string    `json:"registry_status,omitempty"`
	OfficersOnFile     int       `json:"officers_on_file"`
	CompletedAt        time.Time `json:"completed_at"`
}

// BusinessAddressVerificationCompleted: the business's physical address check finished.
type BusinessAddressVerificationCompleted struct {
	OrgID             uuid.UUID `json:"org_id"`
	ApplicationID     uuid.UUID `json:"application_id"`
	Status            string    `json:"status"` // verified | failed
	Provider          string    `json:"provider"`
	ProviderReference string    `json:"provider_reference"`
	Reason            string    `json:"reason,omitempty"`
	CompletedAt       time.Time `json:"completed_at"`
}

// ScreeningCompleted is published after every screening run. It states what was
// checked and what came back, and no conclusion: see ScreeningHitDetected.
//
// The fields down to AdverseMediaMatch are the version-1 payload, unchanged.
type ScreeningCompleted struct {
	// SchemaVersion duplicates the envelope's, as the version-1 payload did.
	// Publishers set it to SchemaVersionScreeningCompleted.
	SchemaVersion int32 `json:"schema_version"`

	CustomerRef string `json:"customer_ref"`
	Phonenumber string `json:"phonenumber"`

	Provider          string    `json:"provider"`
	ProviderReference string    `json:"provider_reference"`
	Country           string    `json:"country"`
	ScreenedAt        time.Time `json:"screened_at"`

	// Status is Clear, Matched, Errored or Skipped. Errored and Skipped are not passes.
	Status string `json:"status"`

	MatchCount        int  `json:"match_count"`
	SanctionsMatch    bool `json:"sanctions_match"`
	PepMatch          bool `json:"pep_match"`
	AdverseMediaMatch bool `json:"adverse_media_match"`

	// Added in version 2.
	OrgID       uuid.UUID `json:"org_id"`
	SubjectKind string    `json:"subject_kind"` // person | entity
	SubjectRef  string    `json:"subject_ref"`
}

// ScreeningMatch is one candidate on a screening hit.
type ScreeningMatch struct {
	MatchID     string   `json:"match_id"`
	ListType    string   `json:"list_type"`
	ListName    string   `json:"list_name"`
	MatchedName string   `json:"matched_name"`
	Score       float64  `json:"score"`
	Countries   []string `json:"countries"`
}

// ScreeningHitDetected announces a match the compliance team has not seen. It
// is an announcement, not an instruction: compliance decides what a hit means.
//
// The fields down to Matches are the version-1 payload, unchanged.
type ScreeningHitDetected struct {
	// SchemaVersion duplicates the envelope's, as the version-1 payload did.
	// Publishers set it to SchemaVersionScreeningHitDetected.
	SchemaVersion int32 `json:"schema_version"`

	CustomerRef string `json:"customer_ref"`
	Phonenumber string `json:"phonenumber"`
	FullName    string `json:"full_name"`
	Country     string `json:"country"`

	Provider          string    `json:"provider"`
	ProviderReference string    `json:"provider_reference"`
	ScreeningResultID string    `json:"screening_result_id"`
	ScreenedAt        time.Time `json:"screened_at"`
	IsRescreen        bool      `json:"is_rescreen"`
	NewMatchIDs       []string  `json:"new_match_ids"`

	Matches []ScreeningMatch `json:"matches"`

	// Added in version 2.
	OrgID       uuid.UUID `json:"org_id"`
	SubjectKind string    `json:"subject_kind"` // person | entity
	SubjectRef  string    `json:"subject_ref"`
}

func (RelatedPartyVerificationCompleted) TopicName() string {
	return TopicRelatedPartyVerificationCompleted
}
func (RelatedPartyVerificationCompleted) PayloadVersion() int32 {
	return SchemaVersionRelatedPartyVerificationCompleted
}
func (LivenessSessionCompleted) TopicName() string     { return TopicLivenessSessionCompleted }
func (LivenessSessionCompleted) PayloadVersion() int32 { return SchemaVersionLivenessSessionCompleted }
func (RegistryLookupCompleted) TopicName() string      { return TopicRegistryLookupCompleted }
func (RegistryLookupCompleted) PayloadVersion() int32  { return SchemaVersionRegistryLookupCompleted }
func (BusinessAddressVerificationCompleted) TopicName() string {
	return TopicBusinessAddressVerificationCompleted
}
func (BusinessAddressVerificationCompleted) PayloadVersion() int32 {
	return SchemaVersionBusinessAddressVerificationCompleted
}
func (ScreeningCompleted) TopicName() string       { return TopicScreeningCompleted }
func (ScreeningCompleted) PayloadVersion() int32   { return SchemaVersionScreeningCompleted }
func (ScreeningHitDetected) TopicName() string     { return TopicScreeningHitDetected }
func (ScreeningHitDetected) PayloadVersion() int32 { return SchemaVersionScreeningHitDetected }
