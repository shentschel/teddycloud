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
