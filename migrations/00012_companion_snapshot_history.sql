-- +goose Up
-- A companion repository row is an immutable resolved snapshot. URL+ref is a
-- discovery target, not an identity: a moving branch must preserve its old
-- row, sessions, context packs, and provider provenance.
ALTER TABLE companion_repositories DROP CONSTRAINT companion_repositories_canonical_url_requested_ref_key;
CREATE INDEX companion_repositories_target_updated_idx ON companion_repositories (canonical_url, requested_ref, updated_at DESC);
CREATE UNIQUE INDEX companion_repositories_snapshot_identity_idx ON companion_repositories (canonical_url, requested_ref, resolved_commit_sha) WHERE resolved_commit_sha <> '';

-- +goose Down
DROP INDEX companion_repositories_snapshot_identity_idx;
DROP INDEX companion_repositories_target_updated_idx;
ALTER TABLE companion_repositories ADD CONSTRAINT companion_repositories_canonical_url_requested_ref_key UNIQUE (canonical_url, requested_ref);
