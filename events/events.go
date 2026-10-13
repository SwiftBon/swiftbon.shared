// Package events holds the payload type for every event topic the web
// dashboard adds, one struct per topic, with the topic name and schema version
// beside it.
//
// Publishers and consumers import the struct instead of describing the JSON in
// their own words. A renamed field then fails a golden-file test here, in one
// place, instead of failing in a consumer in production.
//
// Conventions every payload follows:
//
//   - JSON field names are snake_case.
//   - Every amount is money.Money; no payload has a bare integer amount.
//   - Every payload carries org_id. It is uuid.Nil for the few events that are
//     about the platform rather than a tenant (rail-observation, rail-status-changed,
//     recon-break-detected, safeguarding-breach, and a global setting-changed);
//     the field is still present so consumers can filter on it without a
//     type switch.
//   - Human references (PO-20896, CASE-4182) are strings formatted by package refs.
//   - A change to a published payload may add fields. Renaming, removing or
//     re-typing one needs a new SchemaVersion, and the old version keeps being
//     published until seahorse's min_supported_version moves past it.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Payload is implemented by every event struct in this package.
type Payload interface {
	// TopicName is the seahorse topic the payload is published on.
	TopicName() string
	// Version is the schema version of this struct.
	PayloadVersion() int32
}

// Envelope is the event as it travels between a publisher, seahorse and a
// consumer. Its JSON field names are seahorse's own (seahorse's
// models.PublishRequest and models.ConsumerEvents, migration 000002): topic,
// schema_version, publisher and published_at. The payload travels in seahorse's
// message field. event_id is the one addition, the publisher's outbox row id,
// which consumers deduplicate on; seahorse ignores fields it does not know.
type Envelope struct {
	EventID       uuid.UUID       `json:"event_id"`
	Topic         string          `json:"topic"`
	SchemaVersion int32           `json:"schema_version"`
	OccurredAt    time.Time       `json:"published_at"`
	Producer      string          `json:"publisher"`
	Payload       json.RawMessage `json:"message"`
}

// PublishRequest is the body seahorse's publish endpoint takes: the envelope
// with the payload as a JSON string, which is how seahorse stores it.
type PublishRequest struct {
	Topic         string `json:"topic"`
	Message       string `json:"message"`
	SchemaVersion int32  `json:"schema_version"`
	Publisher     string `json:"publisher"`
}

// NewEnvelope wraps p for publishing. eventID is the outbox row's id, so a
// retried publish carries the same id and the consumer drops the duplicate.
func NewEnvelope(eventID uuid.UUID, producer string, occurredAt time.Time, p Payload) (Envelope, error) {
	if eventID == uuid.Nil {
		return Envelope{}, errors.New("events: envelope needs an event id")
	}
	if producer == "" {
		return Envelope{}, errors.New("events: envelope needs a producer")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, fmt.Errorf("events: marshal %s: %w", p.TopicName(), err)
	}
	return Envelope{
		EventID:       eventID,
		Topic:         p.TopicName(),
		SchemaVersion: p.PayloadVersion(),
		OccurredAt:    occurredAt.UTC(),
		Producer:      producer,
		Payload:       raw,
	}, nil
}

// PublishRequest returns the form seahorse's publish endpoint accepts.
func (e Envelope) PublishRequest() PublishRequest {
	return PublishRequest{
		Topic:         e.Topic,
		Message:       string(e.Payload),
		SchemaVersion: e.SchemaVersion,
		Publisher:     e.Producer,
	}
}

// ErrUnknownTopic is returned by Decode for a topic this package has no type for.
var ErrUnknownTopic = errors.New("events: unknown topic")

// ErrSchemaTooNew is returned by Decode when the envelope was published at a
// schema version newer than this build understands.
var ErrSchemaTooNew = errors.New("events: schema version newer than this build")

// Decode returns the typed payload for e. Unknown JSON fields are tolerated, so
// a consumer on an older build still reads a payload to which fields were
// added; a payload on a newer schema version is refused instead.
func Decode(e Envelope) (Payload, error) {
	newPayload, ok := registry[e.Topic]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTopic, e.Topic)
	}
	p := newPayload()
	if e.SchemaVersion > p.PayloadVersion() {
		return nil, fmt.Errorf("%w: %s v%d, have v%d", ErrSchemaTooNew, e.Topic, e.SchemaVersion, p.PayloadVersion())
	}
	if err := json.Unmarshal(e.Payload, p); err != nil {
		return nil, fmt.Errorf("events: decode %s: %w", e.Topic, err)
	}
	return p, nil
}

// DecodeAs decodes e into the payload type T, for a consumer that already knows
// which topic it handles: DecodeAs[events.HoldPlaced](env). It applies the same
// topic and schema-version checks as Decode.
func DecodeAs[T Payload](e Envelope) (T, error) {
	var v T
	if e.Topic != v.TopicName() {
		return v, fmt.Errorf("events: envelope is topic %q, not %q", e.Topic, v.TopicName())
	}
	if e.SchemaVersion > v.PayloadVersion() {
		return v, fmt.Errorf("%w: %s v%d, have v%d", ErrSchemaTooNew, e.Topic, e.SchemaVersion, v.PayloadVersion())
	}
	if err := json.Unmarshal(e.Payload, &v); err != nil {
		return v, fmt.Errorf("events: decode %s: %w", e.Topic, err)
	}
	return v, nil
}

// Topics lists every topic this package defines, sorted.
func Topics() []string {
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Registered returns a new zero payload for topic.
func Registered(topic string) (Payload, bool) {
	f, ok := registry[topic]
	if !ok {
		return nil, false
	}
	return f(), true
}
