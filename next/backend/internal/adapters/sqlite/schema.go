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
