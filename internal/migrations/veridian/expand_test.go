package veridian

import "testing"

func TestCheckExpandOnly(t *testing.T) {
	ok := []string{
		`CREATE TABLE IF NOT EXISTS veridian_x (id VARCHAR(36) PRIMARY KEY, a TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_x ON veridian_x(a)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_u ON veridian_x(a)`,
		`ALTER TABLE t ADD COLUMN IF NOT EXISTS a TEXT, ADD COLUMN IF NOT EXISTS b INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO settings (key, value) VALUES ('k','v') ON CONFLICT (key) DO NOTHING`,
		"-- commentaire avec DROP dedans\nCREATE TABLE IF NOT EXISTS y (id INT)",
	}
	for _, q := range ok {
		if err := CheckExpandOnly(q); err != nil {
			t.Errorf("devrait passer: %q: %v", q, err)
		}
	}
	bad := map[string]string{
		`DROP TABLE veridian_x`:                                  "drop",
		`ALTER TABLE t DROP COLUMN a`:                            "drop column",
		`TRUNCATE veridian_x`:                                    "truncate",
		`ALTER TABLE t RENAME COLUMN a TO b`:                     "rename",
		`DELETE FROM veridian_x`:                                 "delete",
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t(a)`:      "concurrently",
		`ALTER TABLE t ALTER COLUMN a SET NOT NULL`:              "set not null",
		`ALTER TABLE t ALTER COLUMN a TYPE BIGINT`:               "type",
		`ALTER TABLE t ADD CONSTRAINT c UNIQUE (a)`:              "add constraint",
		`CREATE TABLE veridian_x (id INT)`:                       "create table sans if not exists",
		`CREATE INDEX idx_x ON veridian_x(a)`:                    "create index sans if not exists",
		`ALTER TABLE t ADD COLUMN a TEXT`:                        "add column sans if not exists",
		`ALTER TABLE t ADD COLUMN IF NOT EXISTS a TEXT NOT NULL`: "not null sans default",
		`INSERT INTO settings (key, value) VALUES ('k','v')`:     "insert sans on conflict",
		`   `: "vide",
		`ALTER TABLE t ADD COLUMN IF NOT EXISTS a TEXT; DROP TABLE t`: "drop cache dans une suite",
	}
	for q, why := range bad {
		if err := CheckExpandOnly(q); err == nil {
			t.Errorf("devrait échouer (%s): %q", why, q)
		}
	}
}
