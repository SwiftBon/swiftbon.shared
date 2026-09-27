# 04 — `seahorse` — versioned event contracts for money events

> Read `00-READ-FIRST-shared-context.md` first. Requires `01-shared-module-new.md`.

---

## Prompt

You are working in `/Users/princeokechukwu/source/repos/swiftbon/seahorse`
(module `persianblack.com/seahorse`, Go 1.23.5).

Note the module path and the README title ("Persian Black AccountMs Service", describing
an SMS/email service it is not) — this repo predates the Swiftbon naming and the README
is stale boilerplate. Fix the README as part of this work; leave the module path alone
unless the team wants a rename, since changing it touches every consumer.

### What this service actually is

`seahorse` is a **homegrown Postgres-backed pub/sub broker with gRPC push delivery**. It
is not a gateway, not a BFF, and not a library. It is the event bus for the entire
estate — Kafka and RabbitMQ exist in every repo's `pkg/` and are dead code.

Mechanics:

1. A service calls `RegisterConsumer{ConsumerGroup, Server:"host:port", Topics:[{Name,Ttl}]}`
   at boot, giving seahorse **its own gRPC address**
   (`internal/controller/grpc/seahorse.go:41`).
2. A publisher calls `PublishEvent`. `internal/usecase/publisher.go:12` resolves the
   topic, inserts a `consumer_messages` row, and fans out one `consumer_processing_queue`
   row per registered consumer.
3. The worker (`internal/usecase/worker/consumer.go`) drains the queue and **calls the
   consumer** — `SendConsumerEvent` (`internal/usecase/webapi/consumer.go:17-46`) dials
   the address the consumer registered and invokes `ConsumeEvent`.
4. The consumer acks with `MarkEventAsProcessed`, which inserts into `processed_events`
   guarded by an existence check (`internal/usecase/consumer.go:12-39`).

Delivery is **at-least-once**. Consumers must be idempotent.

Schema: `consumer_groups`, `consumer_topics`, `available_consumers` (with `ttl`),
`consumer_messages`, `consumer_processing_queue` (`message_status`, `retry_count`),
`processed_events`.

### Why this repo is in scope

Seahorse is currency-agnostic — it moves opaque JSON strings and does not need to know
what a currency is. **Do not add currency logic here.**

It is in scope for two reasons:

1. **Two money-path event contracts are changing** and both need a versioned rollout:
   - `account-created` gains `currency` (prompt 02) — postings creates ledger balances
     from it.
   - `transaction-completed` gains `currency` (prompt 03) — communication formats alerts
     from it.
   During rollout, a v1 producer and a v2 consumer will coexist. The bus must not be the
   thing that breaks.
2. **Correctness bugs in the delivery path**, which matter more now that the payloads it
   carries determine ledger currency.

---

## Tasks

### 1. Event envelope versioning

Today an event is `{Id, Topic, Message}` where `Message` is an opaque JSON string. There
is no schema version, so a consumer cannot tell a v1 payload from a v2 one and must
guess from field presence.

Add a version to the envelope — not to each payload, so it works uniformly:

```protobuf
message Event {
  string id             = 1;
  string topic          = 2;
  string message        = 3;
  int32  schema_version = 4;  // 0 = unversioned legacy, treat as 1
  string published_at   = 5;
  string publisher      = 6;  // originating service, for tracing
}
```

`schema_version` absent or `0` means legacy. Existing publishers keep working with no
change; consumers get an explicit signal instead of sniffing fields.

Store `schema_version` and `publisher` on `consumer_messages` so a stuck event can be
diagnosed from the database.

Add this to `swiftbon.shared/proto/seahorse/v1/seahorse.proto` per prompt 01 — the
`.proto` is currently copy-pasted into every consumer repo, so a change here otherwise
means editing it in ten places by hand.

### 2. Topic registry with an owner

`consumer_topics` is just a name. For a money path it should be possible to answer "who
publishes this, and what shape is it".

```sql
ALTER TABLE consumer_topics ADD COLUMN owning_service     VARCHAR(100);
ALTER TABLE consumer_topics ADD COLUMN current_version    INTEGER NOT NULL DEFAULT(1);
ALTER TABLE consumer_topics ADD COLUMN min_supported_version INTEGER NOT NULL DEFAULT(1);
ALTER TABLE consumer_topics ADD COLUMN description        TEXT;
```

Seed the known topics and their owners: `account-created` and
`system-account-created` (account), `transaction-completed` and
`reportable-transaction` (postings), `bvn-verified` /
`means-of-identification-verified` / `id-verification-failed` /
`address-verification-successful` / `address-verification-failed` (kyc), `send-sms` /
`send-email` (communication), `fx-rate-updated` / `fx-rate-stale` / `fx-quote-issued` /
`fx-quote-consumed` / `fx-quote-expired` (fx), `verify-address`, `reverse-transaction`,
`charge-stamp-duty`.

Reject a publish whose `schema_version` is below the topic's `min_supported_version`,
with a clear error rather than a silent drop.

### 3. Correctness fixes in the delivery path

These are real and they sit under the money path.

**Acknowledgement swallows its error.** `internal/usecase/consumer.go:12-39` —
`AcknowledgeMessageAsProcessed` has `return nil` inside an error branch (line 35),
followed by `return err` at 38. A failed acknowledgement reports success, so the event
is neither retried nor recorded. Return the error.

**The existence-check-then-insert on `processed_events` is a race.** Two concurrent
deliveries of the same message to the same consumer group can both pass
`GetProcessedEventByMessageId` and both insert. There is already a
`uc_consumer_message_id_consumer_group_id` unique constraint — rely on it: attempt the
insert and treat a unique violation as success (the event was already processed). Remove
the pre-check.

**Unknown topics churn through retries.** In the consumer services, an unrecognised
topic is marked `EventUnhandled` **with `RetryCount+1`**, so an event nobody handles is
retried until it hits `MaxRetryCount` (10). Seahorse should distinguish "consumer
rejected this permanently" from "delivery failed, retry". Add a terminal status that
does not retry, and expose the count of terminally-rejected events.

**No dead-letter destination.** After `MaxRetryCount` an event stops being retried and
is simply left in place. Add an explicit `message_status` for exhausted, and an endpoint
listing exhausted events so a failed `account-created` (which means a customer has an
account with no ledger balance) is visible rather than buried in a table.

**A metadata request with no `x-client-id` panics in consumers.** Seahorse's own server
guards this correctly with a `len()` check
(`internal/controller/grpc/seahorse.go:32`) — the consumer implementations copied into
every other repo do `md.Get("x-client-id")[0]` and panic. Not fixable from here, but
note it in the README so consumer authors copy the guarded version.

**No timeout headroom on delivery.** `SendConsumerEvent` and the register/ack calls use a
hard `1 * time.Second` context timeout. A posting that takes longer than a second to
process will look like a delivery failure and be retried, producing a duplicate
delivery of a money event. Make the timeout configurable per topic (via
`consumer_topics`), default higher for money topics.

### 4. Observability on the money path

An `account-created` that never gets delivered means a customer has an account and no
balance. That must be loud.

- `GET /seahorse/v1/api/queue/stats` — pending, retrying, exhausted counts per topic.
- `GET /seahorse/v1/api/queue/exhausted` — the events that gave up.
- Prometheus gauges for queue depth per topic and age of the oldest pending event. The
  estate already runs `echoPrometheus` and ships logs to Loki via Grafana Alloy
  (`swiftbon.go.deployment.template/scripts/alloy.config`), so this plugs into existing
  infrastructure.

Note the HTTP surface is currently **entirely commented out**
(`internal/controller/http/v1/seahorse.go:13-25`) — only `/healthz`, `/metrics`, and
`/swagger/*` are live. These new endpoints mean re-enabling that router.

### 5. Rollout sequence for the two changing contracts

Write this into the README as the operational runbook, because getting the order wrong
means either lost events or NGN-labelled CAD balances:

```
1. Deploy seahorse with schema_version on the envelope.        (backward compatible)
2. Deploy postings able to read BOTH:
     schema_version 1 -> currency defaults to NGN, log a warning
     schema_version 2 -> currency read from payload
3. Deploy account publishing schema_version 2 with currency.
4. Confirm zero schema_version-1 account-created events for one full retention window.
5. Raise account-created min_supported_version to 2 and remove the NGN fallback.
```

Same five steps for `transaction-completed` between postings and communication.

Step 2 before step 3 is the part that matters. A v2 producer against a v1 consumer
means postings receives a currency it ignores and creates an NGN balance row for a CAD
account — silent, and expensive to unpick later.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/seahorse
make compose-up && make migrate-up
make test    # -race
```

- Publish a legacy event with no `schema_version`; a consumer receives it with `0`/`1`
  and processes it.
- Publish `schema_version: 2`; the consumer sees `2`.
- Publish below `min_supported_version`; it is rejected with a clear error, not dropped.
- Deliver the same message twice to one consumer group concurrently; exactly one
  `processed_events` row, no error surfaced, no lost ack.
- Make a consumer fail permanently; the event reaches the exhausted status and appears
  in `/queue/exhausted`.
- Make a consumer take 3 seconds on a money topic with a raised per-topic timeout; it
  is not retried and not duplicated.
- `/queue/stats` reports accurate depth per topic.

## Acceptance criteria

- [ ] Envelope carries `schema_version`, `published_at`, and `publisher`; legacy
      publishers keep working unchanged.
- [ ] Topic registry records owner and version bounds; below-minimum publishes are
      rejected explicitly.
- [ ] `AcknowledgeMessageAsProcessed` returns its error.
- [ ] `processed_events` deduplication relies on the unique constraint, not a pre-check;
      the concurrency test passes.
- [ ] Permanently-rejected events do not consume retries.
- [ ] Exhausted events are queryable and counted.
- [ ] Delivery timeout is configurable per topic.
- [ ] Queue depth and oldest-pending-age exported to Prometheus.
- [ ] README documents what seahorse is (replacing the stale SMS/email boilerplate) and
      contains the five-step rollout runbook.

## Out of scope

- Adding currency logic. Seahorse carries opaque payloads; it must stay that way.
- Replacing seahorse with Kafka or a managed broker. It is a real single point of
  failure for money events and worth a design discussion — but that is a separate
  decision, not this change.
- Renaming the module from `persianblack.com/seahorse`.
