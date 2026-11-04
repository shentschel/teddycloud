package sqlite

// assignmentMigration is the staged PI-09/A2a foundation, deliberately absent
// from SchemaMigrations. A2b must activate v10 with the owner and every exact
// catalog schema gate. Calling this function alone grants no persistence port.
//
// Manifest: seven STRICT tables below, five explicit lookup indexes, and ten
// immutable UPDATE/DELETE triggers on identities and retained history. All FKs
// restrict both update and deletion, including same-Tag/aggregate support and
// exact Content/version ownership. No existing object or row is modified.
//
// Canonical blobs are bounded storage envelopes, not SQL proof of their content
// or digest. A2b must validate replay, semantic equivalence, complete prospective
// usage (including command/result, projection and index copies), dependency
// revisions and atomic head/projection installation. Mutable derived rows never
// substitute for retained events. No initial assignments or events are inferred.
func assignmentMigration() Migration {
	m := Migration{Version: 10, ID: "0010-assignment-ledger", Statements: []string{
		`CREATE TABLE tc_assignments (
 assignment_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(assignment_id AS BLOB))=30 AND instr(assignment_id,char(0))=0 AND substr(assignment_id,1,4)='asn_' AND substr(assignment_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 tag_id TEXT NOT NULL UNIQUE REFERENCES tc_tags(tag_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
 UNIQUE(assignment_id,tag_id)
) STRICT`,
		`CREATE TABLE tc_assignment_events (
 assignment_id TEXT NOT NULL,
 tag_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 4096),
 command_id TEXT NOT NULL UNIQUE CHECK(length(CAST(command_id AS BLOB))=32 AND instr(command_id,char(0))=0 AND command_id NOT GLOB '*[^0123456789abcdef]*' AND command_id<>'00000000000000000000000000000000'),
 kind INTEGER NOT NULL CHECK(kind BETWEEN 1 AND 7),
 effective_seconds INTEGER NOT NULL CHECK(effective_seconds BETWEEN -62135596800 AND 253402300799),
 effective_nanos INTEGER NOT NULL CHECK(effective_nanos BETWEEN 0 AND 999999999),
 recorded_seconds INTEGER NOT NULL CHECK(recorded_seconds BETWEEN -62135596800 AND 253402300799),
 recorded_nanos INTEGER NOT NULL CHECK(recorded_nanos BETWEEN 0 AND 999999999),
 content_id TEXT,
 version_id TEXT,
 review_id TEXT UNIQUE CHECK(review_id IS NULL OR (length(CAST(review_id AS BLOB))=30 AND instr(review_id,char(0))=0 AND substr(review_id,1,4)='rev_' AND substr(review_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*')),
 encoding INTEGER NOT NULL CHECK(encoding=1),
 canonical_event BLOB NOT NULL CHECK(length(canonical_event) BETWEEN 1 AND 16777216),
 event_sha256 BLOB NOT NULL CHECK(length(event_sha256)=32),
 new_observations INTEGER NOT NULL CHECK(new_observations BETWEEN 0 AND 64),
 selected_support INTEGER NOT NULL CHECK(selected_support BETWEEN 0 AND 64),
 retained_bytes INTEGER NOT NULL CHECK(retained_bytes BETWEEN 1 AND 16777216),
 CHECK((kind IN (1,5,6) AND content_id IS NOT NULL AND version_id IS NOT NULL) OR (kind NOT IN (1,5,6) AND content_id IS NULL AND version_id IS NULL)),
 CHECK((kind BETWEEN 3 AND 6 AND review_id IS NOT NULL) OR (kind NOT BETWEEN 3 AND 6 AND review_id IS NULL)),
 PRIMARY KEY(assignment_id,revision),
 UNIQUE(assignment_id,tag_id,revision),
 UNIQUE(assignment_id,tag_id,revision,command_id),
 FOREIGN KEY(assignment_id,tag_id) REFERENCES tc_assignments(assignment_id,tag_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
 FOREIGN KEY(content_id) REFERENCES tc_catalog_content(content_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
 FOREIGN KEY(content_id,version_id) REFERENCES tc_content_versions(content_id,version_id) ON UPDATE RESTRICT ON DELETE RESTRICT
) STRICT`,
		`CREATE TABLE tc_assignment_evidence (
 evidence_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(evidence_id AS BLOB))=30 AND instr(evidence_id,char(0))=0 AND substr(evidence_id,1,4)='evd_' AND substr(evidence_id,5) NOT GLOB '*[^0123456789abcdefghjkmnpqrstvwxyz]*'),
 assignment_id TEXT NOT NULL,
 tag_id TEXT NOT NULL,
 introduced_revision INTEGER NOT NULL,
 encoding INTEGER NOT NULL CHECK(encoding=1),
 canonical_evidence BLOB NOT NULL CHECK(length(canonical_evidence) BETWEEN 1 AND 16777216),
 evidence_sha256 BLOB NOT NULL CHECK(length(evidence_sha256)=32),
 content_id TEXT,
 version_id TEXT,
 CHECK((content_id IS NULL AND version_id IS NULL) OR (content_id IS NOT NULL AND version_id IS NOT NULL)),
 UNIQUE(assignment_id,tag_id,evidence_id),
 FOREIGN KEY(assignment_id,tag_id,introduced_revision) REFERENCES tc_assignment_events(assignment_id,tag_id,revision) ON UPDATE RESTRICT ON DELETE RESTRICT,
 FOREIGN KEY(content_id) REFERENCES tc_catalog_content(content_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
 FOREIGN KEY(content_id,version_id) REFERENCES tc_content_versions(content_id,version_id) ON UPDATE RESTRICT ON DELETE RESTRICT
) STRICT`,
		`CREATE TABLE tc_assignment_support (
 assignment_id TEXT NOT NULL,
 tag_id TEXT NOT NULL,
 revision INTEGER NOT NULL,
 evidence_id TEXT NOT NULL,
 PRIMARY KEY(assignment_id,revision,evidence_id),
 FOREIGN KEY(assignment_id,tag_id,revision) REFERENCES tc_assignment_events(assignment_id,tag_id,revision) ON UPDATE RESTRICT ON DELETE RESTRICT,
 FOREIGN KEY(assignment_id,tag_id,evidence_id) REFERENCES tc_assignment_evidence(assignment_id,tag_id,evidence_id) ON UPDATE RESTRICT ON DELETE RESTRICT
) STRICT`,
		`CREATE TABLE tc_assignment_commands (
 command_id TEXT PRIMARY KEY NOT NULL,
 assignment_id TEXT NOT NULL,
 tag_id TEXT NOT NULL,
 revision INTEGER NOT NULL,
 encoding INTEGER NOT NULL CHECK(encoding=1),
 canonical_command BLOB NOT NULL CHECK(length(canonical_command) BETWEEN 1 AND 16777216),
 canonical_result BLOB NOT NULL CHECK(length(canonical_result) BETWEEN 1 AND 16777216),
 command_sha256 BLOB NOT NULL CHECK(length(command_sha256)=32),
 result_sha256 BLOB NOT NULL CHECK(length(result_sha256)=32),
 UNIQUE(assignment_id,revision),
 FOREIGN KEY(assignment_id,tag_id,revision,command_id) REFERENCES tc_assignment_events(assignment_id,tag_id,revision,command_id) ON UPDATE RESTRICT ON DELETE RESTRICT
) STRICT`,
		`CREATE TABLE tc_assignment_heads (
 assignment_id TEXT PRIMARY KEY NOT NULL,
 tag_id TEXT NOT NULL UNIQUE,
 head_revision INTEGER NOT NULL CHECK(head_revision BETWEEN 1 AND 4096),
 retired INTEGER NOT NULL CHECK(retired IN (0,1)),
 event_count INTEGER NOT NULL CHECK(event_count=head_revision),
 observation_count INTEGER NOT NULL CHECK(observation_count BETWEEN 0 AND 4096),
 support_count INTEGER NOT NULL CHECK(support_count BETWEEN 0 AND 16384),
 retained_bytes INTEGER NOT NULL CHECK(retained_bytes BETWEEN 1 AND 16777216),
 FOREIGN KEY(assignment_id,tag_id,head_revision) REFERENCES tc_assignment_events(assignment_id,tag_id,revision) ON UPDATE RESTRICT ON DELETE RESTRICT
) STRICT`,
		`CREATE TABLE tc_assignment_projections (
 assignment_id TEXT PRIMARY KEY NOT NULL,
 tag_id TEXT NOT NULL UNIQUE,
 head_revision INTEGER NOT NULL,
 encoding INTEGER NOT NULL CHECK(encoding=1),
 canonical_projection BLOB NOT NULL CHECK(length(canonical_projection) BETWEEN 1 AND 16777216),
 projection_sha256 BLOB NOT NULL CHECK(length(projection_sha256)=32),
 FOREIGN KEY(assignment_id,tag_id,head_revision) REFERENCES tc_assignment_events(assignment_id,tag_id,revision) ON UPDATE RESTRICT ON DELETE RESTRICT
) STRICT`,
		`CREATE INDEX tc_assignment_events_effective ON tc_assignment_events(assignment_id,effective_seconds,effective_nanos,revision)`,
		`CREATE INDEX tc_assignment_events_target ON tc_assignment_events(content_id,version_id)`,
		`CREATE INDEX tc_assignment_evidence_history ON tc_assignment_evidence(assignment_id,introduced_revision,evidence_id)`,
		`CREATE INDEX tc_assignment_evidence_target ON tc_assignment_evidence(content_id,version_id)`,
		`CREATE INDEX tc_assignment_support_evidence ON tc_assignment_support(assignment_id,tag_id,evidence_id)`,
	}}
	// Values follow application Kind: Assign, Unassign, RecordClassification,
	// ResolveClassification, RecordOriginalTarget, ResolveOriginalTarget, Retire.
	// Heads/projections are rebuildable; identities/history are retained forever.
	for _, table := range []string{"tc_assignments", "tc_assignment_events", "tc_assignment_evidence", "tc_assignment_support", "tc_assignment_commands"} {
		for _, op := range []struct{ suffix, sql string }{{"update", "UPDATE"}, {"delete", "DELETE"}} {
			m.Statements = append(m.Statements, "CREATE TRIGGER "+table+"_no_"+op.suffix+" BEFORE "+op.sql+" ON "+table+" BEGIN SELECT RAISE(ABORT,'immutable assignment history'); END")
		}
	}
	return m
}
