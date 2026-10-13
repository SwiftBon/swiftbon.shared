package events_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"swiftbon.com/shared/events"
	"swiftbon.com/shared/money"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// wantTopics is the topic list in the dashboard context (00 §7). A topic added
// there must be added here and to the package; a topic dropped must be dropped
// from all three.
var wantTopics = []string{
	// identity
	"organisation-created", "member-invited", "member-joined", "member-role-changed",
	"member-removed", "session-revoked", "mfa-reset-requested",
	// onboarding
	"application-submitted", "kyb-review-requested", "application-approved",
	"application-declined", "rfi-opened", "rfi-responded", "rfi-overdue",
	// kyc
	"related-party-verification-completed", "liveness-session-completed",
	"registry-lookup-completed", "business-address-verification-completed",
	"screening-completed", "screening-hit-detected",
	// documents
	"document-checked", "bundle-split", "document-rendered",
	// account
	"business-created", "business-account-opened", "receiving-details-issued", "capability-changed",
	// postings
	"hold-placed", "hold-released", "hold-captured", "inbound-credited",
	// rails
	"provider-subaccount-status-changed", "provider-rfi-opened", "provider-capability-changed",
	"inbound-payment-received", "inbound-payment-pending", "payout-status-changed", "rail-observation",
	// approvals
	"approval-requested", "approval-decided", "approval-expired",
	// compliance
	"case-opened", "case-dispositioned", "audit-recorded",
	// payouts
	"beneficiary-status-changed", "payout-status-changed-public",
	// support
	"support-case-opened", "support-case-updated",
	// treasury
	"rail-status-changed", "recon-break-detected", "safeguarding-breach",
	// communication, settings
	"notification-requested", "setting-changed",
}

var fixedTime = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// fill populates every field of v deterministically, so a golden file shows the
// full shape of a payload and a field with a wrong or missing tag cannot hide.
func fill(v reflect.Value, path string) {
	switch v.Type() {
	case reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(fixedTime))
		return
	case reflect.TypeOf(uuid.UUID{}):
		v.Set(reflect.ValueOf(uuid.NewSHA1(uuid.NameSpaceOID, []byte(path))))
		return
	case reflect.TypeOf(money.Money{}):
		v.Set(reflect.ValueOf(money.Money{Amount: 125000, Currency: "NGN"}))
		return
	case reflect.TypeOf(json.RawMessage{}):
		v.Set(reflect.ValueOf(json.RawMessage(`{"key":"value"}`)))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(strings.ToLower(path[strings.LastIndex(path, ".")+1:]))
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(3)
	case reflect.Float64:
		v.SetFloat(0.5)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), path)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0), path+"[0]")
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		val := reflect.New(v.Type().Elem()).Elem()
		fill(val, path+".value")
		m.SetMapIndex(reflect.ValueOf("key"), val)
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), path+"."+v.Type().Field(i).Name)
			}
		}
	default:
		panic("events_test: cannot fill " + v.Type().String() + " at " + path)
	}
}

func sample(t *testing.T, topic string) events.Payload {
	t.Helper()
	p, ok := events.Registered(topic)
	if !ok {
		t.Fatalf("topic %q not registered", topic)
	}
	fill(reflect.ValueOf(p).Elem(), reflect.TypeOf(p).Elem().Name())
	return p
}

func TestRegistryCoversEveryTopic(t *testing.T) {
	got := events.Topics()
	want := append([]string(nil), wantTopics...)
	if len(got) != len(want) {
		t.Errorf("registry has %d topics, 00 §7 lists %d", len(got), len(want))
	}
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("topic %q has no payload type", w)
		}
		delete(have, w)
	}
	for extra := range have {
		t.Errorf("topic %q is defined but not in the 00 §7 list", extra)
	}
}

var kebab = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)
var snake = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

func TestPayloadContract(t *testing.T) {
	for _, topic := range events.Topics() {
		p, _ := events.Registered(topic)
		if p.TopicName() != topic {
			t.Errorf("%T: TopicName() = %q, registered as %q", p, p.TopicName(), topic)
		}
		if !kebab.MatchString(topic) {
			t.Errorf("topic %q is not kebab-case", topic)
		}
		if p.PayloadVersion() < 1 {
			t.Errorf("%T: schema version %d < 1", p, p.PayloadVersion())
		}
		// Every payload carries org_id.
		f, ok := reflect.TypeOf(p).Elem().FieldByName("OrgID")
		if !ok || f.Type != reflect.TypeOf(uuid.UUID{}) || f.Tag.Get("json") != "org_id" {
			t.Errorf("%T: missing OrgID uuid.UUID `json:\"org_id\"`", p)
		}
	}
}

func TestNoBareAmounts(t *testing.T) {
	// An integer field named like an amount would be a number with no currency.
	amountish := regexp.MustCompile(`(?i)(amount|balance|fee|liabilit|shortfall|funds)`)
	for _, topic := range events.Topics() {
		p, _ := events.Registered(topic)
		rt := reflect.TypeOf(p).Elem()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !amountish.MatchString(f.Name) {
				continue
			}
			switch f.Type.Kind() {
			case reflect.Int, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64, reflect.String:
				if f.Type.Kind() == reflect.String && !strings.Contains(strings.ToLower(f.Name), "amount") {
					continue
				}
				t.Errorf("%s.%s is %s; amounts must be money.Money", rt.Name(), f.Name, f.Type)
			}
		}
	}
}

func goldenPath(topic string) string { return filepath.Join("testdata", topic+".golden.json") }

func marshalGolden(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestGolden(t *testing.T) {
	for _, topic := range events.Topics() {
		t.Run(topic, func(t *testing.T) {
			got := marshalGolden(t, sample(t, topic))
			path := goldenPath(topic)
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden file (run go test ./events -update): %v", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Errorf("payload JSON changed for %s; if intended, bump the schema version or confirm the change is additive, then run go test ./events -update\n got: %s\nwant: %s", topic, got, want)
			}
		})
	}
}

func TestGoldenFilesAreSnakeCase(t *testing.T) {
	var walk func(t *testing.T, topic, path string, v any)
	walk = func(t *testing.T, topic, path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				// "vars" is a free-form map keyed by template variable.
				if !snake.MatchString(k) {
					t.Errorf("%s: key %q at %s is not snake_case", topic, k, path)
				}
				walk(t, topic, path+"."+k, child)
			}
		case []any:
			for _, child := range x {
				walk(t, topic, path+"[]", child)
			}
		}
	}
	for _, topic := range events.Topics() {
		raw, err := os.ReadFile(goldenPath(topic))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		walk(t, topic, "", v)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, topic := range events.Topics() {
		t.Run(topic, func(t *testing.T) {
			orig := sample(t, topic)
			raw, err := json.Marshal(orig)
			if err != nil {
				t.Fatal(err)
			}
			back, _ := events.Registered(topic)
			if err := json.Unmarshal(raw, back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(orig, back) {
				t.Errorf("round trip changed the payload:\n orig: %+v\n back: %+v", orig, back)
			}
			// The golden file itself decodes and re-encodes to itself.
			golden, err := os.ReadFile(goldenPath(topic))
			if err != nil {
				t.Fatal(err)
			}
			fromGolden, _ := events.Registered(topic)
			if err := json.Unmarshal(golden, fromGolden); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(marshalGolden(t, fromGolden), bytes.ReplaceAll(golden, []byte("\r\n"), []byte("\n"))) {
				t.Error("golden file does not survive decode and re-encode")
			}
		})
	}
}

func TestEnvelope(t *testing.T) {
	id := uuid.New()
	p := sample(t, events.TopicHoldPlaced)
	env, err := events.NewEnvelope(id, "swiftbon.postings.service", fixedTime.In(time.FixedZone("x", 3600)), p)
	if err != nil {
		t.Fatal(err)
	}
	if env.Topic != "hold-placed" || env.SchemaVersion != 1 || env.EventID != id ||
		env.Producer != "swiftbon.postings.service" || env.OccurredAt.Location() != time.UTC {
		t.Errorf("envelope = %+v", env)
	}

	// The wire names are seahorse's.
	raw, _ := json.Marshal(env)
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	for _, k := range []string{"topic", "schema_version", "publisher", "published_at", "message", "event_id"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("envelope JSON lacks %q: %s", k, raw)
		}
	}

	pr := env.PublishRequest()
	if pr.Topic != "hold-placed" || pr.SchemaVersion != 1 || pr.Publisher != env.Producer || pr.Message != string(env.Payload) {
		t.Errorf("publish request = %+v", pr)
	}
	prRaw, _ := json.Marshal(pr)
	if !strings.Contains(string(prRaw), `"message":"{`) {
		t.Errorf("seahorse takes the message as a JSON string: %s", prRaw)
	}

	got, err := events.DecodeAs[events.HoldPlaced](env)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, p) {
		t.Errorf("DecodeAs = %+v, want %+v", got, p)
	}
	any, err := events.Decode(env)
	if err != nil || !reflect.DeepEqual(any, p) {
		t.Errorf("Decode = %+v, %v", any, err)
	}
}

func TestEnvelopeValidation(t *testing.T) {
	p := &events.HoldPlaced{}
	if _, err := events.NewEnvelope(uuid.Nil, "svc", fixedTime, p); err == nil {
		t.Error("nil event id accepted")
	}
	if _, err := events.NewEnvelope(uuid.New(), "", fixedTime, p); err == nil {
		t.Error("empty producer accepted")
	}
}

func TestDecodeErrors(t *testing.T) {
	good := func() events.Envelope {
		env, err := events.NewEnvelope(uuid.New(), "svc", fixedTime, sample(t, events.TopicHoldPlaced))
		if err != nil {
			t.Fatal(err)
		}
		return env
	}

	e := good()
	e.Topic = "no-such-topic"
	if _, err := events.Decode(e); !errors.Is(err, events.ErrUnknownTopic) {
		t.Errorf("unknown topic: %v", err)
	}

	e = good()
	e.SchemaVersion = 99
	if _, err := events.Decode(e); !errors.Is(err, events.ErrSchemaTooNew) {
		t.Errorf("newer schema: %v", err)
	}
	if _, err := events.DecodeAs[events.HoldPlaced](e); !errors.Is(err, events.ErrSchemaTooNew) {
		t.Errorf("DecodeAs newer schema: %v", err)
	}

	e = good()
	e.Payload = json.RawMessage(`{"hold_id": 7}`)
	if _, err := events.Decode(e); err == nil {
		t.Error("bad payload accepted by Decode")
	}
	if _, err := events.DecodeAs[events.HoldPlaced](e); err == nil {
		t.Error("bad payload accepted by DecodeAs")
	}

	e = good()
	if _, err := events.DecodeAs[events.HoldReleased](e); err == nil {
		t.Error("DecodeAs accepted a different topic")
	}
}

func TestOlderConsumerToleratesAddedFields(t *testing.T) {
	e := events.Envelope{
		Topic: events.TopicSessionRevoked, SchemaVersion: 1,
		Payload: json.RawMessage(`{"org_id":"` + uuid.NewString() + `","session_id":"s1","a_field_added_later":true}`),
	}
	got, err := events.DecodeAs[events.SessionRevoked](e)
	if err != nil || got.SessionID != "s1" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestLegacyScreeningFieldsUnchanged(t *testing.T) {
	// The version-1 payload from swiftbon.kyc.service, as it is published today.
	legacy := `{"schema_version":1,"customer_ref":"+2348012345678","phonenumber":"+2348012345678",
		"provider":"smileid","provider_reference":"p1","country":"NG","screened_at":"2026-10-01T10:00:00Z",
		"status":"Matched","match_count":2,"sanctions_match":true,"pep_match":false,"adverse_media_match":false}`
	var got events.ScreeningCompleted
	if err := json.Unmarshal([]byte(legacy), &got); err != nil {
		t.Fatal(err)
	}
	if got.CustomerRef != "+2348012345678" || got.Status != "Matched" || got.MatchCount != 2 || !got.SanctionsMatch || got.OrgID != uuid.Nil {
		t.Errorf("legacy payload misread: %+v", got)
	}
}
