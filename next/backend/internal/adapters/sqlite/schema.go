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
