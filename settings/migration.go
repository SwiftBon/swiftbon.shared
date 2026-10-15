package settings

// MigrationUp is the migration template every service copies into its own
// migrations directory (as, for example, 000020_settings.up.sql). The table is
// the same in every service; each service owns its own copy of it.
//
// Two departures from the bare column list in the design, both deliberate:
//
//   - UNIQUE (key, scope_org_id, version) does not stop two global rows
//     (scope_org_id NULL) sharing a version, because NULLs never compare equal in
//     a unique constraint. The expression index below is the one that enforces it.
//   - Rows are immutable. A trigger refuses UPDATE and DELETE, because history is
//     how "which threshold applied to this payout on 3 March?" is answered.
const MigrationUp = `CREATE TABLE IF NOT EXISTS settings (
    key                 TEXT        NOT NULL,
    scope_org_id        UUID        NULL,
    version             BIGINT      NOT NULL CHECK (version >= 1),
    value               JSONB       NOT NULL,
    effective_from      TIMESTAMPTZ NOT NULL,
    changed_by          TEXT        NOT NULL,
    approval_request_id UUID        NULL,
    reason              TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT settings_key_org_version_uq UNIQUE (key, scope_org_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS settings_key_scope_version_uq
    ON settings (key, COALESCE(scope_org_id, '00000000-0000-0000-0000-000000000000'::uuid), version);

CREATE INDEX IF NOT EXISTS settings_effective_idx
    ON settings (effective_from);

CREATE OR REPLACE FUNCTION settings_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'settings rows are immutable; append a new version instead';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS settings_no_update ON settings;
CREATE TRIGGER settings_no_update BEFORE UPDATE OR DELETE ON settings
    FOR EACH ROW EXECUTE FUNCTION settings_immutable();
`

// MigrationDown reverses MigrationUp.
const MigrationDown = `DROP TRIGGER IF EXISTS settings_no_update ON settings;
DROP FUNCTION IF EXISTS settings_immutable();
DROP TABLE IF EXISTS settings;
`
