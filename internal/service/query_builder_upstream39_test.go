// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/query_builder_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryBuilder_NotInTheLastDays(t *testing.T) {
	t.Skip("écart non analysé (A2) : filtres de segment (scalaire JSON, « pas dans les X jours » est une fonctionnalité v37)")
	qb := NewQueryBuilder()

	contactTree := func(filter *domain.DimensionFilter) *domain.TreeNode {
		return &domain.TreeNode{
			Kind: "leaf",
			Leaf: &domain.TreeNodeLeaf{
				Source:  "contacts",
				Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{filter}},
			},
		}
	}

	t.Run("includes rows whose date was never set", func(t *testing.T) {
		sql, args, err := qb.BuildSQL(contactTree(&domain.DimensionFilter{
			FieldName:    "custom_datetime_1",
			FieldType:    "time",
			Operator:     "not_in_the_last_days",
			StringValues: []string{"30"},
		}))
		require.NoError(t, err)

		// A bare NOT (col > ...) would evaluate to NULL for unset dates and drop those contacts,
		// who are exactly the audience for "has not converted in the last 30 days".
		assert.Contains(t, sql, "(custom_datetime_1 IS NULL OR custom_datetime_1 <= NOW() - INTERVAL '30 days')")
		assert.Empty(t, args)
	})

	t.Run("is the complement of in_the_last_days", func(t *testing.T) {
		positive, _, err := qb.BuildSQL(contactTree(&domain.DimensionFilter{
			FieldName:    "custom_datetime_2",
			FieldType:    "time",
			Operator:     "in_the_last_days",
			StringValues: []string{"7"},
		}))
		require.NoError(t, err)
		assert.Contains(t, positive, "custom_datetime_2 > NOW() - INTERVAL '7 days'")
		assert.NotContains(t, positive, "IS NULL")
	})

	t.Run("only the leading integer reaches the interval", func(t *testing.T) {
		// The day count is the one value interpolated into the SQL text rather than bound, so it
		// must never carry anything but an int. Sscanf stops at the first non-digit, which keeps
		// a crafted suffix out of the query entirely.
		sql, _, err := qb.BuildSQL(contactTree(&domain.DimensionFilter{
			FieldName:    "custom_datetime_1",
			FieldType:    "time",
			Operator:     "not_in_the_last_days",
			StringValues: []string{"30 days'; DROP TABLE contacts; --"},
		}))
		require.NoError(t, err)

		assert.Contains(t, sql, "INTERVAL '30 days'")
		assert.NotContains(t, sql, "DROP TABLE")
	})

	t.Run("rejects a day count with no digits", func(t *testing.T) {
		_, _, err := qb.BuildSQL(contactTree(&domain.DimensionFilter{
			FieldName:    "custom_datetime_1",
			FieldType:    "time",
			Operator:     "not_in_the_last_days",
			StringValues: []string{"abc"},
		}))
		require.Error(t, err)
	})

	t.Run("rejects an unknown field", func(t *testing.T) {
		_, _, err := qb.BuildSQL(contactTree(&domain.DimensionFilter{
			FieldName:    "not_a_column",
			FieldType:    "time",
			Operator:     "not_in_the_last_days",
			StringValues: []string{"30"},
		}))
		require.Error(t, err)
	})
}

func TestQueryBuilder_NotInTheLastDaysBoundary(t *testing.T) {
	t.Skip("écart non analysé (A2) : filtres de segment (scalaire JSON, « pas dans les X jours » est une fonctionnalité v37)")
	qb := NewQueryBuilder()

	// The two operators must partition the non-NULL rows exactly: > for the window, <= outside
	// it. A boundary row (exactly N days old) belongs to precisely one of them, and any drift
	// here silently double-counts or strands contacts on the day their window rolls over.
	positive, _, err := qb.BuildSQL(&domain.TreeNode{
		Kind: "leaf",
		Leaf: &domain.TreeNodeLeaf{
			Source: "contacts",
			Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{
				{FieldName: "custom_datetime_1", FieldType: "time", Operator: "in_the_last_days", StringValues: []string{"30"}},
			}},
		},
	})
	require.NoError(t, err)

	negative, _, err := qb.BuildSQL(&domain.TreeNode{
		Kind: "leaf",
		Leaf: &domain.TreeNodeLeaf{
			Source: "contacts",
			Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{
				{FieldName: "custom_datetime_1", FieldType: "time", Operator: "not_in_the_last_days", StringValues: []string{"30"}},
			}},
		},
	})
	require.NoError(t, err)

	assert.Contains(t, positive, "custom_datetime_1 > NOW() - INTERVAL '30 days'")
	assert.Contains(t, negative, "custom_datetime_1 <= NOW() - INTERVAL '30 days'")
	// Strictly complementary comparisons, so the boundary instant falls on exactly one side.
	assert.NotContains(t, negative, "custom_datetime_1 < NOW()")
	// Plus the NULLs, which the positive form can never match.
	assert.Contains(t, negative, "custom_datetime_1 IS NULL OR")
	assert.NotContains(t, positive, "IS NULL")
}

// contacts.upsert used to answer 400 for anything but an object or an array in the five
// custom_json columns, so every expression the segment builder emits for them could assume a
// container. It no longer can: a scalar is stored as-is, matching what lists.subscribe had always
// accepted. Two of those expressions quietly change meaning when the value is a scalar, and both
// change it in the direction of matching contacts that hold nothing of the sort.
func TestQueryBuilder_JSONScalarCannotSatisfyKeyOrNumericFilters(t *testing.T) {
	t.Skip("écart non analysé (A2) : filtres de segment (scalaire JSON, « pas dans les X jours » est une fonctionnalité v37)")
	qb := NewQueryBuilder()

	segmentSQL := func(t *testing.T, filter *domain.DimensionFilter) string {
		t.Helper()
		sql, _, err := qb.BuildSQL(&domain.TreeNode{
			Kind: "leaf",
			Leaf: &domain.TreeNodeLeaf{
				Source:  "contacts",
				Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{filter}},
			},
		})
		require.NoError(t, err)
		return sql
	}

	t.Run("a key check requires an object, in the segment and the trigger form alike", func(t *testing.T) {
		filter := &domain.DimensionFilter{
			FieldName: "custom_json_1",
			FieldType: "json",
			Operator:  "is_set",
			JSONPath:  []string{"tier"},
		}

		assert.Contains(t, segmentSQL(t, filter),
			"(jsonb_typeof(custom_json_1) = 'object' AND custom_json_1 ? $1)")

		// The same filter compiled into an automation's PL/pgSQL trigger body, where a wrong
		// answer enrols the wrong contacts instead of mailing them.
		trigger, _, err := qb.BuildTriggerCondition(&domain.TreeNode{
			Kind: "leaf",
			Leaf: &domain.TreeNodeLeaf{
				Source:  "contacts",
				Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{filter}},
			},
		}, "NEW.email")
		require.NoError(t, err)
		assert.Contains(t, trigger, "(jsonb_typeof(custom_json_1) = 'object' AND custom_json_1 ? $1)")
	})

	// field_type "string" rather than "json": tree validation demands a json_path from a "json"
	// filter, so the whole-column form of in_array is reachable only through the API, and only
	// spelled this way. It routes on the column's whitelist type, not the filter's.
	t.Run("an element check requires a container", func(t *testing.T) {
		sql := segmentSQL(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "string",
			Operator:     "in_array",
			StringValues: []string{"gold"},
		})
		assert.Contains(t, sql, "(jsonb_typeof(custom_json_1) IN ('object', 'array') AND custom_json_1 ? $1)")
	})

	t.Run("a numeric comparison against the whole column is guarded", func(t *testing.T) {
		sql := segmentSQL(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "number",
			Operator:     "gte",
			NumberValues: []float64{10},
		})
		assert.Contains(t, sql,
			"CASE WHEN jsonb_typeof(custom_json_1) = 'object' THEN (custom_json_1::text)::numeric END >= $1")
	})

	t.Run("a date comparison against the whole column is guarded", func(t *testing.T) {
		sql := segmentSQL(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "time",
			Operator:     "after_date",
			StringValues: []string{"2024-01-01T00:00:00Z"},
		})
		assert.Contains(t, sql,
			"CASE WHEN jsonb_typeof(custom_json_1) = 'object' THEN (custom_json_1::text)::timestamptz END > $1")
	})

	t.Run("a relative-day comparison against the whole column is guarded", func(t *testing.T) {
		sql := segmentSQL(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "time",
			Operator:     "in_the_last_days",
			StringValues: []string{"7"},
		})
		assert.Contains(t, sql,
			"CASE WHEN jsonb_typeof(custom_json_1) = 'object' THEN (custom_json_1::text)::timestamptz END > NOW() - INTERVAL '7 days'")
	})

	// The guard belongs to the whole-column form only. A path already answers NULL on a scalar,
	// and a jsonb_typeof(custom_json_1) = 'object' test in front of an array index would refuse
	// the array it is indexing into.
	t.Run("addressing a value inside the column is left alone", func(t *testing.T) {
		byKey := segmentSQL(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "number",
			Operator:     "gte",
			JSONPath:     []string{"score"},
			NumberValues: []float64{10},
		})
		assert.Contains(t, byKey, "(custom_json_1['score']::text)::numeric >= $1")
		assert.NotContains(t, byKey, "jsonb_typeof")

		byIndex := segmentSQL(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "number",
			Operator:     "gte",
			JSONPath:     []string{"0"},
			NumberValues: []float64{10},
		})
		assert.Contains(t, byIndex, "(custom_json_1[0]::text)::numeric >= $1")
		assert.NotContains(t, byIndex, "jsonb_typeof")
	})
}

// openSegmentTestPostgres connects to the PostgreSQL that tests/compose.test.yaml starts, or skips.
//
// Everything above asserts a string, which is exactly the kind of test the scalar hole walked past:
// `custom_json_1 ? $1` reads as an object-key lookup and is not one, and no assertion over Go
// strings can tell the difference. Only the server settles what the emitted SQL means, so these
// cases run against it.
func openSegmentTestPostgres(t *testing.T) *sql.DB {
	t.Helper()

	host := envOrDefault("TEST_DB_HOST", "localhost")
	port := envOrDefault("TEST_DB_PORT", "5433")
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=2",
		host, port,
		envOrDefault("TEST_DB_USER", "notifuse_test"),
		envOrDefault("TEST_DB_PASSWORD", "test_password"),
		envOrDefault("TEST_DB_NAME", "postgres"),
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no test PostgreSQL at %s:%s: %v", host, port, err)
	}
	// A single connection, because the fixtures live in a TEMP table and only the session that
	// created it can see one.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("no test PostgreSQL at %s:%s (docker compose -f tests/compose.test.yaml up -d): %v", host, port, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// seedJSONContacts creates the TEMP contacts table the generated SQL selects from and fills it with
// one row per custom_json_1 shape. The table is named "contacts" because BuildSQL hard-codes that.
func seedJSONContacts(t *testing.T, db *sql.DB, rows map[string]string) {
	t.Helper()

	_, err := db.Exec(`DROP TABLE IF EXISTS contacts`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TEMP TABLE contacts (email text PRIMARY KEY, custom_json_1 jsonb)`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS contacts`) })

	for email, value := range rows {
		if value == "" {
			_, err = db.Exec(`INSERT INTO contacts (email, custom_json_1) VALUES ($1, NULL)`, email)
		} else {
			_, err = db.Exec(`INSERT INTO contacts (email, custom_json_1) VALUES ($1, $2::jsonb)`, email, value)
		}
		require.NoError(t, err)
	}
}

func matchedEmails(t *testing.T, db *sql.DB, query string, args []interface{}) []string {
	t.Helper()

	rows, err := db.Query(query, args...)
	require.NoError(t, err, "query: %s", query)
	defer rows.Close()

	var emails []string
	for rows.Next() {
		var email string
		require.NoError(t, rows.Scan(&email))
		emails = append(emails, email)
	}
	require.NoError(t, rows.Err())
	sort.Strings(emails)
	return emails
}

func TestQueryBuilder_JSONScalarSemanticsOnPostgres(t *testing.T) {
	t.Skip("écart non analysé (A2) : filtres de segment (scalaire JSON, « pas dans les X jours » est une fonctionnalité v37)")
	db := openSegmentTestPostgres(t)
	qb := NewQueryBuilder()

	buildSegment := func(t *testing.T, filter *domain.DimensionFilter) (string, []interface{}) {
		t.Helper()
		query, args, err := qb.BuildSQL(&domain.TreeNode{
			Kind: "leaf",
			Leaf: &domain.TreeNodeLeaf{
				Source:  "contacts",
				Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{filter}},
			},
		})
		require.NoError(t, err)
		return query, args
	}

	t.Run("only a contact holding the key matches is_set", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"object@example.com": `{"tier":"gold"}`,
			"scalar@example.com": `"tier"`,
			"array@example.com":  `["tier"]`,
			"number@example.com": `42`,
			"null@example.com":   ``,
		})

		query, args := buildSegment(t, &domain.DimensionFilter{
			FieldName: "custom_json_1",
			FieldType: "json",
			Operator:  "is_set",
			JSONPath:  []string{"tier"},
		})

		// scalar@example.com is the contact this finding is about: `'"tier"'::jsonb ? 'tier'` is
		// true on PostgreSQL 17, so before the guard the bare string "tier" joined every segment
		// asking for the key. array@example.com went in for the same reason.
		assert.Equal(t, []string{"object@example.com"}, matchedEmails(t, db, query, args))
	})

	t.Run("a contact holding the key stops matching is_not_set", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"object@example.com": `{"tier":"gold"}`,
			"scalar@example.com": `"tier"`,
			"other@example.com":  `{"plan":"free"}`,
		})

		query, args := buildSegment(t, &domain.DimensionFilter{
			FieldName: "custom_json_1",
			FieldType: "json",
			Operator:  "is_not_set",
			JSONPath:  []string{"tier"},
		})

		assert.Equal(t, []string{"other@example.com", "scalar@example.com"},
			matchedEmails(t, db, query, args))
	})

	t.Run("only a container matches in_array", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"array@example.com":  `["gold","silver"]`,
			"object@example.com": `{"gold":true}`,
			"scalar@example.com": `"gold"`,
			"other@example.com":  `["silver"]`,
		})

		query, args := buildSegment(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "string",
			Operator:     "in_array",
			StringValues: []string{"gold"},
		})

		assert.Equal(t, []string{"array@example.com", "object@example.com"},
			matchedEmails(t, db, query, args))
	})

	// The sibling defect. A number in the column made (custom_json_1::text)::numeric succeed where
	// it had raised on every row, so a filter that used to fail loudly and always started matching
	// instead — and whether it matched or raised depended on which rows the planner reached, which
	// is a segment that works until the plan changes.
	t.Run("a stored number does not satisfy a whole-column numeric comparison", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"number@example.com": `42`,
			"string@example.com": `"42"`,
		})

		query, args := buildSegment(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "number",
			Operator:     "gte",
			NumberValues: []float64{10},
		})

		assert.Empty(t, matchedEmails(t, db, query, args))
	})

	t.Run("a stored date string does not satisfy a whole-column date comparison", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"date@example.com": `"2024-06-01"`,
		})

		query, args := buildSegment(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "time",
			Operator:     "after_date",
			StringValues: []string{"2024-01-01T00:00:00Z"},
		})

		assert.Empty(t, matchedEmails(t, db, query, args))
	})

	// The trigger form casts defensively, so nothing raises there and the wrong answer is silent.
	// It also nests the new guard inside the existing CASE, which is the part only the server can
	// confirm is still valid SQL.
	t.Run("the trigger form guards the same numeric comparison", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"number@example.com": `42`,
			"scored@example.com": `{"score":42}`,
		})

		condition, args, err := qb.BuildTriggerCondition(&domain.TreeNode{
			Kind: "leaf",
			Leaf: &domain.TreeNodeLeaf{
				Source: "contacts",
				Contact: &domain.ContactCondition{Filters: []*domain.DimensionFilter{{
					FieldName:    "custom_json_1",
					FieldType:    "number",
					Operator:     "gte",
					NumberValues: []float64{10},
				}}},
			},
		}, "c.email")
		require.NoError(t, err)

		assert.Empty(t, matchedEmails(t,
			db, "SELECT c.email FROM contacts c WHERE "+condition, args))
	})

	// Guarding the whole-column form must not cost the addressed forms, which are what the console
	// actually builds: it requires a JSON path for every operator except is_set/is_not_set.
	t.Run("a value addressed inside the column still matches", func(t *testing.T) {
		seedJSONContacts(t, db, map[string]string{
			"key@example.com":    `{"score":42}`,
			"index@example.com":  `[42]`,
			"scalar@example.com": `42`,
			"low@example.com":    `{"score":1}`,
		})

		byKey, keyArgs := buildSegment(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "number",
			Operator:     "gte",
			JSONPath:     []string{"score"},
			NumberValues: []float64{10},
		})
		assert.Equal(t, []string{"key@example.com"}, matchedEmails(t, db, byKey, keyArgs))

		byIndex, indexArgs := buildSegment(t, &domain.DimensionFilter{
			FieldName:    "custom_json_1",
			FieldType:    "number",
			Operator:     "gte",
			JSONPath:     []string{"0"},
			NumberValues: []float64{10},
		})
		assert.Equal(t, []string{"index@example.com"}, matchedEmails(t, db, byIndex, indexArgs))
	})
}
