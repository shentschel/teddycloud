package sqlite

func eventMigration() Migration {
	return Migration{Version: 11, ID: "0011-event-journal", Statements: []string{
		`CREATE TABLE tc_event_meta (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 tenant TEXT NOT NULL CHECK(length(tenant)=32 AND tenant NOT GLOB '*[^0-9a-f]*' AND tenant<>printf('%032d',0)),
 instance TEXT NOT NULL CHECK(length(instance)=32 AND instance NOT GLOB '*[^0-9a-f]*' AND instance<>printf('%032d',0)),
 signing_key BLOB NOT NULL CHECK(length(signing_key)=32),
 authorization_generation INTEGER NOT NULL CHECK(authorization_generation>0)
 ) STRICT`,
		`CREATE TABLE tc_event_streams (
 scope TEXT PRIMARY KEY NOT NULL CHECK(scope='control' OR (length(scope)=32 AND scope NOT GLOB '*[^0-9a-f]*' AND scope<>printf('%032d',0))),
 epoch TEXT NOT NULL UNIQUE CHECK(length(epoch)=32 AND epoch NOT GLOB '*[^0-9a-f]*' AND epoch<>printf('%032d',0)),
 high_water INTEGER NOT NULL CHECK(high_water>=0),
 floor INTEGER NOT NULL CHECK(floor>=0 AND floor<=high_water),
 retained_rows INTEGER NOT NULL CHECK(retained_rows BETWEEN 0 AND 4096),
 retained_bytes INTEGER NOT NULL CHECK(retained_bytes BETWEEN 0 AND 4194304),
 UNIQUE(scope,epoch)
 ) STRICT`,
		`CREATE TABLE tc_event_sources (
 owner TEXT PRIMARY KEY NOT NULL CHECK(owner IN ('tag-registry','content-store','catalog','assignment','gateway-observation','lifecycle')),
 epoch TEXT NOT NULL UNIQUE CHECK(length(epoch)=32 AND epoch NOT GLOB '*[^0-9a-f]*' AND epoch<>printf('%032d',0)),
 sequence INTEGER NOT NULL CHECK(sequence>=0)
 ) STRICT`,
		`CREATE TABLE tc_event_journal (
 scope TEXT NOT NULL REFERENCES tc_event_streams(scope) ON DELETE RESTRICT,
 epoch TEXT NOT NULL,
 sequence INTEGER NOT NULL CHECK(sequence>0),
 committed_at TEXT NOT NULL CHECK(length(committed_at)=30),
 envelope BLOB NOT NULL CHECK(length(envelope) BETWEEN 1 AND 8192),
 PRIMARY KEY(epoch,sequence),
 UNIQUE(scope,sequence),
 FOREIGN KEY(scope,epoch) REFERENCES tc_event_streams(scope,epoch) ON DELETE RESTRICT
 ) STRICT`,
		`CREATE INDEX tc_event_journal_age ON tc_event_journal(committed_at)`,
		`CREATE TABLE tc_event_observations (
 box TEXT PRIMARY KEY NOT NULL REFERENCES tc_event_streams(scope) ON DELETE RESTRICT CHECK(length(box)=32 AND box NOT GLOB '*[^0-9a-f]*' AND box<>printf('%032d',0)),
 lease TEXT NOT NULL UNIQUE CHECK(length(lease)=32 AND lease NOT GLOB '*[^0-9a-f]*' AND lease<>printf('%032d',0)),
 sequence INTEGER NOT NULL CHECK(sequence>=0),
 revision INTEGER NOT NULL CHECK(revision>0),
 fresh INTEGER NOT NULL CHECK(fresh IN (0,1)),
 placement BLOB CHECK(placement IS NULL OR length(placement) BETWEEN 1 AND 4096),
 playback BLOB CHECK(playback IS NULL OR length(playback) BETWEEN 1 AND 4096),
 placement_lease TEXT,
 playback_lease TEXT,
 last_input BLOB CHECK(last_input IS NULL OR length(last_input) BETWEEN 1 AND 4096),
 last_result BLOB CHECK(last_result IS NULL OR length(last_result) BETWEEN 1 AND 8192),
 CHECK((last_input IS NULL AND last_result IS NULL) OR (last_input IS NOT NULL AND last_result IS NOT NULL))
 ) STRICT`,
	}}
}
