package sqlite

var schemaMigrations = []Migration{
	{
		Version: 1,
		ID:      "0001-catalog-content",
		Statements: []string{
			`CREATE TABLE tc_catalog_content (
				content_id TEXT PRIMARY KEY NOT NULL,
				title TEXT NOT NULL,
				model_number TEXT,
				article_number TEXT
			) STRICT`,
		},
	},
	{
		Version: 2,
		ID:      "0002-tag-identity",
		Statements: []string{
			`CREATE TABLE tc_tags (
				tag_id TEXT PRIMARY KEY NOT NULL CHECK (
					length(tag_id) = 30
					AND substr(tag_id, 1, 4) = 'tag_'
					AND substr(tag_id, 5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'
				),
				uid BLOB NOT NULL UNIQUE CHECK (
					typeof(uid) = 'blob'
					AND length(uid) = 8
				),
				revision INTEGER NOT NULL CHECK (
					typeof(revision) = 'integer'
					AND revision > 0
				)
			) STRICT`,
		},
	},
	{
		Version: 3,
		ID:      "0003-tag-metadata",
		Statements: []string{
			`CREATE TABLE tc_tag_observations (
    observation_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(observation_id AS BLOB))=30 AND substr(observation_id,1,4)='obs_' AND substr(observation_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
    tag_id TEXT NOT NULL REFERENCES tc_tags(tag_id) ON DELETE RESTRICT,
    introduced_revision INTEGER NOT NULL CHECK(introduced_revision>=2),
    fact_key INTEGER NOT NULL CHECK(fact_key BETWEEN 0 AND 3),
    value INTEGER NOT NULL CHECK(value IN (0,1)),
    source_name TEXT NOT NULL CHECK(length(CAST(source_name AS BLOB)) BETWEEN 1 AND 128),
    source_revision TEXT NOT NULL CHECK(length(CAST(source_revision AS BLOB)) BETWEEN 1 AND 128),
    source_record TEXT NOT NULL CHECK(length(CAST(source_record AS BLOB)) BETWEEN 1 AND 128),
    observed_at TEXT NOT NULL CHECK(length(CAST(observed_at AS BLOB))=30),
    confidence INTEGER NOT NULL CHECK(confidence BETWEEN 0 AND 2),
    review INTEGER NOT NULL CHECK(review BETWEEN 0 AND 3),
    UNIQUE(tag_id,observation_id)
   ) STRICT`,
			`CREATE INDEX tc_tag_observations_tag ON tc_tag_observations(tag_id,fact_key,introduced_revision)`,
			`CREATE TABLE tc_tag_decisions (
    decision_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(decision_id AS BLOB))=30 AND substr(decision_id,1,4)='dec_' AND substr(decision_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
    tag_id TEXT NOT NULL REFERENCES tc_tags(tag_id) ON DELETE RESTRICT,
    fact_key INTEGER NOT NULL CHECK(fact_key BETWEEN 0 AND 3),
    expected_revision INTEGER NOT NULL CHECK(expected_revision>0 AND expected_revision<9223372036854775807),
    result_revision INTEGER NOT NULL CHECK(result_revision=expected_revision+1),
    UNIQUE(tag_id,decision_id),
    UNIQUE(tag_id,result_revision)
   ) STRICT`,
			`CREATE INDEX tc_tag_decisions_tag ON tc_tag_decisions(tag_id,fact_key,result_revision)`,
			`CREATE TABLE tc_tag_decision_support (
    tag_id TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    observation_id TEXT NOT NULL,
    PRIMARY KEY(decision_id,observation_id),
    FOREIGN KEY(tag_id,decision_id) REFERENCES tc_tag_decisions(tag_id,decision_id) ON DELETE RESTRICT,
    FOREIGN KEY(tag_id,observation_id) REFERENCES tc_tag_observations(tag_id,observation_id) ON DELETE RESTRICT
   ) STRICT`,
			`CREATE INDEX tc_tag_support_tag ON tc_tag_decision_support(tag_id,decision_id)`,
		},
	},
	{
		Version: 4,
		ID:      "0004-content-blobs",
		Statements: []string{
			`CREATE TABLE tc_blobs (
 digest BLOB PRIMARY KEY NOT NULL CHECK(typeof(digest)='blob' AND length(digest)=32),
 algorithm INTEGER NOT NULL CHECK(algorithm=1),
 size INTEGER NOT NULL CHECK(size BETWEEN 4097 AND 1073741824),
 profile INTEGER NOT NULL CHECK(profile=1)
) STRICT`,
			`CREATE TABLE tc_content_versions (
 version_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(version_id AS BLOB))=30 AND instr(version_id,char(0))=0 AND substr(version_id,1,4)='ver_' AND substr(version_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 content_id TEXT NOT NULL REFERENCES tc_catalog_content(content_id) ON DELETE RESTRICT CHECK(length(CAST(content_id AS BLOB))=30 AND instr(content_id,char(0))=0 AND substr(content_id,1,4)='cnt_' AND substr(content_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 audio_id INTEGER NOT NULL CHECK(audio_id BETWEEN 1 AND 4294967295),
 audio_sha1 BLOB NOT NULL CHECK(typeof(audio_sha1)='blob' AND length(audio_sha1)=20),
 order_known INTEGER NOT NULL CHECK(order_known IN (0,1)),
 order_namespace TEXT,
 order_position BLOB,
 CHECK((order_known=0 AND order_namespace IS NULL AND order_position IS NULL) OR
 (order_known=1 AND order_namespace IS NOT NULL AND length(CAST(order_namespace AS BLOB)) BETWEEN 1 AND 128 AND order_namespace NOT GLOB '*[^!-~]*' AND instr(order_namespace,char(0))=0 AND typeof(order_position)='blob' AND length(order_position)=8)),
 UNIQUE(content_id,version_id)
) STRICT`,
			`CREATE TABLE tc_version_blobs (
 version_id TEXT PRIMARY KEY NOT NULL REFERENCES tc_content_versions(version_id) ON DELETE RESTRICT CHECK(length(CAST(version_id AS BLOB))=30 AND instr(version_id,char(0))=0 AND substr(version_id,1,4)='ver_' AND substr(version_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 digest BLOB NOT NULL REFERENCES tc_blobs(digest) ON DELETE RESTRICT CHECK(typeof(digest)='blob' AND length(digest)=32)
) STRICT`,
			`CREATE TABLE tc_blob_imports (
 import_key TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(import_key AS BLOB))=30 AND instr(import_key,char(0))=0 AND substr(import_key,1,4)='imp_' AND substr(import_key,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 encoding INTEGER NOT NULL CHECK(encoding=1),
 command BLOB NOT NULL CHECK(typeof(command)='blob' AND length(command) BETWEEN 137 AND 275),
 fingerprint BLOB NOT NULL CHECK(typeof(fingerprint)='blob' AND length(fingerprint)=32),
 version_id TEXT NOT NULL REFERENCES tc_version_blobs(version_id) ON DELETE RESTRICT CHECK(length(CAST(version_id AS BLOB))=30 AND instr(version_id,char(0))=0 AND substr(version_id,1,4)='ver_' AND substr(version_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*')
) STRICT`,
		},
	},
	// PI-08/A2.1 reserves v5 for the normalized review foundation only.
	// Source rows describe core bindings; loading text never grants a capability.
	// Migration does not adopt, review or protect any existing subject. Order,
	// staging reports and guarded repository behavior remain later checkpoints.
	{
		Version: 5,
		ID:      "0005-catalog-provenance",
		Statements: []string{
			`CREATE TABLE tc_catalog_sources (
 source_id TEXT NOT NULL CHECK(length(source_id)=32 AND source_id NOT GLOB '*[^0123456789abcdef]*' AND source_id <> '00000000000000000000000000000000'),
 registration_revision TEXT NOT NULL CHECK(length(registration_revision)=32 AND registration_revision NOT GLOB '*[^0123456789abcdef]*' AND registration_revision <> '00000000000000000000000000000000'),
 issuer_principal TEXT NOT NULL CHECK(length(issuer_principal)=32 AND issuer_principal NOT GLOB '*[^0123456789abcdef]*' AND issuer_principal <> '00000000000000000000000000000000'),
 adapter TEXT NOT NULL CHECK(length(CAST(adapter AS BLOB)) BETWEEN 1 AND 128 AND instr(adapter,char(0))=0),
 adapter_version TEXT NOT NULL CHECK(length(CAST(adapter_version AS BLOB)) BETWEEN 1 AND 128 AND instr(adapter_version,char(0))=0),
 scope TEXT NOT NULL CHECK(length(CAST(scope AS BLOB)) BETWEEN 1 AND 128 AND instr(scope,char(0))=0),
 acquisition INTEGER NOT NULL CHECK(acquisition BETWEEN 1 AND 5),
 revoked INTEGER NOT NULL CHECK(revoked IN (0,1)),
 PRIMARY KEY(source_id,registration_revision),
 UNIQUE(source_id,registration_revision,acquisition)
) STRICT`,
			`CREATE TABLE tc_catalog_source_claims (
 source_id TEXT NOT NULL,
 registration_revision TEXT NOT NULL,
 acquisition INTEGER NOT NULL,
 claim_key INTEGER NOT NULL CHECK(claim_key BETWEEN 1 AND 5),
 CHECK(acquisition<>4 AND (acquisition NOT IN (1,2) OR claim_key<>5)),
 PRIMARY KEY(source_id,registration_revision,claim_key),
 FOREIGN KEY(source_id,registration_revision,acquisition) REFERENCES tc_catalog_sources(source_id,registration_revision,acquisition) ON DELETE RESTRICT
) STRICT`,
			`CREATE TABLE tc_catalog_batches (
 batch_id TEXT PRIMARY KEY NOT NULL CHECK(length(batch_id)=32 AND batch_id NOT GLOB '*[^0123456789abcdef]*' AND batch_id <> '00000000000000000000000000000000'),
 source_id TEXT NOT NULL,
 registration_revision TEXT NOT NULL,
 artifact_sha256 BLOB NOT NULL CHECK(length(artifact_sha256)=32 AND artifact_sha256<>zeroblob(32)),
 byte_length INTEGER NOT NULL CHECK(byte_length BETWEEN 1 AND 1048576),
 record_count INTEGER NOT NULL CHECK(record_count BETWEEN 1 AND 32),
 parser_revision TEXT NOT NULL CHECK(length(CAST(parser_revision AS BLOB)) BETWEEN 1 AND 128 AND instr(parser_revision,char(0))=0),
 source_revision TEXT NOT NULL CHECK(length(CAST(source_revision AS BLOB)) BETWEEN 0 AND 128 AND instr(source_revision,char(0))=0),
 UNIQUE(batch_id,source_id,registration_revision),
 FOREIGN KEY(source_id,registration_revision) REFERENCES tc_catalog_sources(source_id,registration_revision) ON DELETE RESTRICT
) STRICT`,
			`CREATE TABLE tc_catalog_records (
 batch_id TEXT NOT NULL REFERENCES tc_catalog_batches(batch_id) ON DELETE RESTRICT,
 record_index INTEGER NOT NULL CHECK(record_index BETWEEN 0 AND 31),
 locator TEXT NOT NULL CHECK(length(CAST(locator AS BLOB)) BETWEEN 1 AND 128 AND instr(locator,char(0))=0),
 snapshot BLOB NOT NULL CHECK(length(snapshot) BETWEEN 1 AND 65536),
 PRIMARY KEY(batch_id,record_index)
) STRICT`,
			`CREATE TRIGGER tc_catalog_record_bounds BEFORE INSERT ON tc_catalog_records
 WHEN NOT EXISTS(SELECT 1 FROM tc_catalog_batches WHERE batch_id=NEW.batch_id AND NEW.record_index<record_count AND length(NEW.snapshot)<=byte_length)
 BEGIN SELECT RAISE(ABORT,'catalog record bounds'); END`,
			`CREATE TABLE tc_catalog_managed (
 content_id TEXT PRIMARY KEY NOT NULL REFERENCES tc_catalog_content(content_id) ON DELETE RESTRICT CHECK(length(CAST(content_id AS BLOB))=30 AND instr(content_id,char(0))=0 AND substr(content_id,1,4)='cnt_' AND substr(content_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 aggregate_revision INTEGER NOT NULL CHECK(aggregate_revision>0),
 protected_custom INTEGER NOT NULL CHECK(protected_custom IN (0,1))
) STRICT`,
			`CREATE TABLE tc_catalog_subjects (
 subject_id TEXT PRIMARY KEY NOT NULL,
 content_id TEXT NOT NULL REFERENCES tc_catalog_managed(content_id) ON DELETE RESTRICT,
 version_id TEXT,
 review_revision INTEGER NOT NULL CHECK(review_revision>0),
 CHECK((version_id IS NULL AND subject_id=content_id) OR (version_id IS NOT NULL AND subject_id=version_id)),
 UNIQUE(subject_id,content_id),
 FOREIGN KEY(content_id,version_id) REFERENCES tc_content_versions(content_id,version_id) ON DELETE RESTRICT
) STRICT`,
			`CREATE TABLE tc_catalog_observations (
 observation_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(observation_id AS BLOB))=30 AND instr(observation_id,char(0))=0 AND substr(observation_id,1,4)='obs_' AND substr(observation_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 subject_id TEXT NOT NULL,
 content_id TEXT NOT NULL,
 claim_key INTEGER NOT NULL CHECK(claim_key BETWEEN 1 AND 5),
 text_value TEXT,
 audio_id BLOB,
 audio_sha1 BLOB,
 owner_content_id TEXT REFERENCES tc_catalog_content(content_id) ON DELETE RESTRICT,
 batch_id TEXT NOT NULL,
 record_index INTEGER NOT NULL,
 source_id TEXT NOT NULL,
 registration_revision TEXT NOT NULL,
 asserted_origin TEXT NOT NULL CHECK(length(CAST(asserted_origin AS BLOB)) BETWEEN 0 AND 128 AND instr(asserted_origin,char(0))=0),
 observed_at TEXT NOT NULL CHECK(length(CAST(observed_at AS BLOB)) BETWEEN 1 AND 128 AND instr(observed_at,char(0))=0),
 confidence INTEGER NOT NULL CHECK(confidence BETWEEN 0 AND 2),
 CHECK(claim_key NOT IN (2,3) OR (length(CAST(text_value AS BLOB)) BETWEEN 1 AND 64 AND text_value NOT GLOB '*[^a-zA-Z0-9_.-]*')),
 CHECK(
  (claim_key IN (1,2,3) AND subject_id=content_id AND text_value IS NOT NULL AND length(CAST(text_value AS BLOB)) BETWEEN 1 AND 4096 AND instr(text_value,char(0))=0 AND audio_id IS NULL AND audio_sha1 IS NULL AND owner_content_id IS NULL) OR
  (claim_key=4 AND subject_id<>content_id AND text_value IS NULL AND audio_id IS NOT NULL AND length(audio_id)=8 AND audio_id<>zeroblob(8) AND audio_sha1 IS NOT NULL AND length(audio_sha1)=20 AND owner_content_id IS NULL) OR
  (claim_key=5 AND subject_id<>content_id AND text_value IS NULL AND audio_id IS NULL AND audio_sha1 IS NULL AND owner_content_id IS NOT NULL AND owner_content_id=content_id)
 ),
 UNIQUE(subject_id,source_id,registration_revision,claim_key,observation_id),
 FOREIGN KEY(subject_id,content_id) REFERENCES tc_catalog_subjects(subject_id,content_id) ON DELETE RESTRICT,
 FOREIGN KEY(batch_id,record_index) REFERENCES tc_catalog_records(batch_id,record_index) ON DELETE RESTRICT,
 FOREIGN KEY(batch_id,source_id,registration_revision) REFERENCES tc_catalog_batches(batch_id,source_id,registration_revision) ON DELETE RESTRICT,
 FOREIGN KEY(source_id,registration_revision,claim_key) REFERENCES tc_catalog_source_claims(source_id,registration_revision,claim_key) ON DELETE RESTRICT
) STRICT`,
			`CREATE INDEX tc_catalog_observations_subject ON tc_catalog_observations(subject_id,claim_key,observation_id)`,
			`CREATE TABLE tc_catalog_decisions (
 command_id TEXT PRIMARY KEY NOT NULL CHECK(length(command_id)=32 AND command_id NOT GLOB '*[^0123456789abcdef]*' AND command_id <> '00000000000000000000000000000000'),
 subject_id TEXT NOT NULL,
 content_id TEXT NOT NULL,
 source_id TEXT NOT NULL,
 registration_revision TEXT NOT NULL,
 reviewer_principal TEXT NOT NULL CHECK(length(reviewer_principal)=32 AND reviewer_principal NOT GLOB '*[^0123456789abcdef]*' AND reviewer_principal <> '00000000000000000000000000000000'),
 policy_revision TEXT NOT NULL CHECK(policy_revision='pi08-catalog/1'),
 kind INTEGER NOT NULL CHECK(kind BETWEEN 1 AND 3),
 claim_key INTEGER NOT NULL,
 expected_aggregate_revision INTEGER NOT NULL CHECK(expected_aggregate_revision>0 AND expected_aggregate_revision<9223372036854775807),
 result_aggregate_revision INTEGER NOT NULL CHECK(result_aggregate_revision=expected_aggregate_revision+1),
 expected_review_revision INTEGER NOT NULL CHECK(expected_review_revision>0 AND expected_review_revision<9223372036854775807),
 result_review_revision INTEGER NOT NULL CHECK(result_review_revision=expected_review_revision+1),
 reason TEXT NOT NULL CHECK(length(CAST(reason AS BLOB)) BETWEEN 1 AND 4096 AND instr(reason,char(0))=0),
 canonical_command BLOB NOT NULL CHECK(length(canonical_command) BETWEEN 1 AND 8388608),
 canonical_result BLOB NOT NULL CHECK(length(canonical_result) BETWEEN 1 AND 8388608),
 CHECK((kind=1 AND claim_key=0) OR (kind=2 AND claim_key BETWEEN 1 AND 5) OR (kind=3 AND claim_key BETWEEN 1 AND 3)),
 UNIQUE(content_id,result_aggregate_revision),
 UNIQUE(subject_id,result_review_revision),
 UNIQUE(subject_id,source_id,registration_revision,claim_key,command_id),
 FOREIGN KEY(subject_id,content_id) REFERENCES tc_catalog_subjects(subject_id,content_id) ON DELETE RESTRICT,
 FOREIGN KEY(source_id,registration_revision) REFERENCES tc_catalog_sources(source_id,registration_revision) ON DELETE RESTRICT
) STRICT`,
			`CREATE TABLE tc_catalog_decision_support (
 subject_id TEXT NOT NULL,
 source_id TEXT NOT NULL,
 registration_revision TEXT NOT NULL,
 claim_key INTEGER NOT NULL CHECK(claim_key BETWEEN 1 AND 5),
 command_id TEXT NOT NULL,
 observation_id TEXT NOT NULL,
 PRIMARY KEY(command_id,observation_id),
 FOREIGN KEY(subject_id,source_id,registration_revision,claim_key,command_id) REFERENCES tc_catalog_decisions(subject_id,source_id,registration_revision,claim_key,command_id) ON DELETE RESTRICT,
 FOREIGN KEY(subject_id,source_id,registration_revision,claim_key,observation_id) REFERENCES tc_catalog_observations(subject_id,source_id,registration_revision,claim_key,observation_id) ON DELETE RESTRICT
) STRICT`,
			`CREATE INDEX tc_catalog_support_subject ON tc_catalog_decision_support(subject_id,command_id)`,
			`CREATE TRIGGER tc_catalog_batches_no_update BEFORE UPDATE ON tc_catalog_batches
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_batches_no_delete BEFORE DELETE ON tc_catalog_batches
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_records_no_update BEFORE UPDATE ON tc_catalog_records
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_records_no_delete BEFORE DELETE ON tc_catalog_records
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_observations_no_update BEFORE UPDATE ON tc_catalog_observations
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_observations_no_delete BEFORE DELETE ON tc_catalog_observations
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_decisions_no_update BEFORE UPDATE ON tc_catalog_decisions
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_decisions_no_delete BEFORE DELETE ON tc_catalog_decisions
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_decision_support_no_update BEFORE UPDATE ON tc_catalog_decision_support
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_decision_support_no_delete BEFORE DELETE ON tc_catalog_decision_support
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_source_claims_no_update BEFORE UPDATE ON tc_catalog_source_claims
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_source_claims_no_delete BEFORE DELETE ON tc_catalog_source_claims
 BEGIN SELECT RAISE(ABORT,'immutable catalog evidence'); END`,
			`CREATE TRIGGER tc_catalog_source_binding_immutable BEFORE UPDATE ON tc_catalog_sources
 WHEN NEW.source_id<>OLD.source_id OR NEW.registration_revision<>OLD.registration_revision OR NEW.issuer_principal<>OLD.issuer_principal OR NEW.adapter<>OLD.adapter OR NEW.adapter_version<>OLD.adapter_version OR NEW.scope<>OLD.scope OR NEW.acquisition<>OLD.acquisition OR NEW.revoked<OLD.revoked
 BEGIN SELECT RAISE(ABORT,'immutable catalog source binding'); END`,
			`CREATE TRIGGER tc_catalog_source_no_delete BEFORE DELETE ON tc_catalog_sources
 BEGIN SELECT RAISE(ABORT,'retained catalog source'); END`,
			`CREATE TRIGGER tc_catalog_protection_monotonic BEFORE UPDATE ON tc_catalog_managed
 WHEN NEW.content_id<>OLD.content_id OR NEW.protected_custom<OLD.protected_custom OR NEW.aggregate_revision<OLD.aggregate_revision
 BEGIN SELECT RAISE(ABORT,'catalog managed state regression'); END`,
			`CREATE TRIGGER tc_catalog_managed_no_delete BEFORE DELETE ON tc_catalog_managed
 BEGIN SELECT RAISE(ABORT,'retained catalog managed state'); END`,
			`CREATE TRIGGER tc_catalog_subject_binding_immutable BEFORE UPDATE ON tc_catalog_subjects
 WHEN NEW.subject_id<>OLD.subject_id OR NEW.content_id<>OLD.content_id OR NEW.version_id IS NOT OLD.version_id OR NEW.review_revision<OLD.review_revision
 BEGIN SELECT RAISE(ABORT,'catalog subject state regression'); END`,
			`CREATE TRIGGER tc_catalog_subject_no_delete BEFORE DELETE ON tc_catalog_subjects
 BEGIN SELECT RAISE(ABORT,'retained catalog subject'); END`,
		},
	},
	// Bounded persistence envelopes, not capabilities. The future adapter must
	// verify SHA-256, decode with A1.3c and cross-check normalized evidence and
	// the complete version manifest in the same transaction before using state.
	{
		Version: 6,
		ID:      "0006-catalog-aggregate-state",
		Statements: []string{
			`CREATE TABLE tc_catalog_review_states (
 subject_id TEXT NOT NULL,
 content_id TEXT NOT NULL,
 aggregate_revision INTEGER NOT NULL CHECK(aggregate_revision>0),
 review_revision INTEGER NOT NULL CHECK(review_revision BETWEEN 1 AND 1025),
 encoding INTEGER NOT NULL CHECK(encoding=1),
 state BLOB NOT NULL CHECK(length(state) BETWEEN 112 AND 8388608),
 state_bytes INTEGER NOT NULL CHECK(state_bytes=length(state)),
 state_sha256 BLOB NOT NULL CHECK(length(state_sha256)=32 AND state_sha256<>zeroblob(32)),
 CHECK(hex(substr(state,1,64))='0000000000000013706930382D7265766965772D73746174652F31000000000000001E'||hex(CAST(content_id AS BLOB))),
 CHECK(hex(substr(state,65,8))=CASE WHEN subject_id=content_id THEN '0000000000000000' ELSE '000000000000001E' END),
 CHECK(subject_id=content_id OR substr(state,73,30)=CAST(subject_id AS BLOB)),
 CHECK(hex(substr(state,CASE WHEN subject_id=content_id THEN 73 ELSE 103 END,8))=printf('%016X',review_revision)),
 PRIMARY KEY(subject_id,review_revision),
 UNIQUE(subject_id,aggregate_revision),
 FOREIGN KEY(subject_id,content_id) REFERENCES tc_catalog_subjects(subject_id,content_id) ON DELETE RESTRICT
) STRICT`,
			`CREATE TRIGGER tc_catalog_review_state_append BEFORE INSERT ON tc_catalog_review_states
 WHEN NOT EXISTS(SELECT 1 FROM tc_catalog_managed m JOIN tc_catalog_subjects s ON s.content_id=m.content_id WHERE s.subject_id=NEW.subject_id AND m.content_id=NEW.content_id AND m.aggregate_revision=NEW.aggregate_revision AND s.review_revision=NEW.review_revision)
 OR EXISTS(SELECT 1 FROM tc_catalog_review_states WHERE subject_id=NEW.subject_id AND (review_revision>=NEW.review_revision OR aggregate_revision>=NEW.aggregate_revision))
 BEGIN SELECT RAISE(ABORT,'catalog review state revision or binding'); END`,
			`CREATE TABLE tc_catalog_order_states (
 content_id TEXT NOT NULL REFERENCES tc_catalog_managed(content_id) ON DELETE RESTRICT,
 aggregate_revision INTEGER NOT NULL CHECK(aggregate_revision>0),
 order_revision INTEGER NOT NULL CHECK(order_revision BETWEEN 1 AND 1025),
 encoding INTEGER NOT NULL CHECK(encoding=1),
 state BLOB NOT NULL CHECK(length(state) BETWEEN 103 AND 8388608),
 state_bytes INTEGER NOT NULL CHECK(state_bytes=length(state)),
 state_sha256 BLOB NOT NULL CHECK(length(state_sha256)=32 AND state_sha256<>zeroblob(32)),
 CHECK(hex(substr(state,1,63))='0000000000000012706930382D6F726465722D73746174652F31000000000000001E'||hex(CAST(content_id AS BLOB))),
 CHECK(hex(substr(state,64,8))=printf('%016X',order_revision)),
 PRIMARY KEY(content_id,order_revision),
 UNIQUE(content_id,aggregate_revision)
) STRICT`,
			`CREATE TRIGGER tc_catalog_order_state_append BEFORE INSERT ON tc_catalog_order_states
 WHEN NOT EXISTS(SELECT 1 FROM tc_catalog_managed WHERE content_id=NEW.content_id AND aggregate_revision=NEW.aggregate_revision)
 OR EXISTS(SELECT 1 FROM tc_catalog_order_states WHERE content_id=NEW.content_id AND (order_revision>=NEW.order_revision OR aggregate_revision>=NEW.aggregate_revision))
 BEGIN SELECT RAISE(ABORT,'catalog order state revision or binding'); END`,
			`CREATE TABLE tc_catalog_order_state_versions (
 content_id TEXT NOT NULL,
 order_revision INTEGER NOT NULL,
 version_id TEXT NOT NULL,
 PRIMARY KEY(content_id,order_revision,version_id),
 FOREIGN KEY(content_id,order_revision) REFERENCES tc_catalog_order_states(content_id,order_revision) ON DELETE RESTRICT,
 FOREIGN KEY(content_id,version_id) REFERENCES tc_content_versions(content_id,version_id) ON DELETE RESTRICT
) STRICT`,
			`CREATE TRIGGER tc_catalog_order_state_version_bounds BEFORE INSERT ON tc_catalog_order_state_versions
 WHEN (SELECT count(*) FROM tc_catalog_order_state_versions WHERE content_id=NEW.content_id AND order_revision=NEW.order_revision)>=1024
 OR EXISTS(SELECT 1 FROM tc_catalog_order_state_versions WHERE content_id=NEW.content_id AND order_revision=NEW.order_revision AND version_id=NEW.version_id)
 BEGIN SELECT RAISE(ABORT,'catalog order state version bounds'); END`,
			`CREATE TRIGGER tc_catalog_review_states_no_update BEFORE UPDATE ON tc_catalog_review_states
 BEGIN SELECT RAISE(ABORT,'immutable catalog state'); END`,
			`CREATE TRIGGER tc_catalog_review_states_no_delete BEFORE DELETE ON tc_catalog_review_states
 BEGIN SELECT RAISE(ABORT,'immutable catalog state'); END`,
			`CREATE TRIGGER tc_catalog_order_states_no_update BEFORE UPDATE ON tc_catalog_order_states
 BEGIN SELECT RAISE(ABORT,'immutable catalog state'); END`,
			`CREATE TRIGGER tc_catalog_order_states_no_delete BEFORE DELETE ON tc_catalog_order_states
 BEGIN SELECT RAISE(ABORT,'immutable catalog state'); END`,
			`CREATE TRIGGER tc_catalog_order_state_versions_no_update BEFORE UPDATE ON tc_catalog_order_state_versions
 BEGIN SELECT RAISE(ABORT,'immutable catalog state'); END`,
			`CREATE TRIGGER tc_catalog_order_state_versions_no_delete BEFORE DELETE ON tc_catalog_order_state_versions
 BEGIN SELECT RAISE(ABORT,'immutable catalog state'); END`,
		},
	},
}

// SchemaMigrations returns a copy of the ordered, forward-only application
// schema. Applied migration checksums make later mutation fail closed.
func SchemaMigrations() []Migration {
	result := make([]Migration, len(schemaMigrations))
	for index, migration := range schemaMigrations {
		result[index] = migration
		result[index].Statements = append([]string(nil), migration.Statements...)
	}
	return result
}
