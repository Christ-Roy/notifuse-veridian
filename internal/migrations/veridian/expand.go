package veridian

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reLineComment  = regexp.MustCompile(`--[^\n]*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reSpaces       = regexp.MustCompile(`\s+`)

	reBanned = []struct {
		re  *regexp.Regexp
		msg string
	}{
		{regexp.MustCompile(`\bDROP\b`), "DROP est une étape CONTRACT"},
		{regexp.MustCompile(`\bTRUNCATE\b`), "TRUNCATE est destructif"},
		{regexp.MustCompile(`\bRENAME\b`), "RENAME casse le tag précédent (expand: ajouter, copier, contract: retirer)"},
		{regexp.MustCompile(`\bDELETE\s+FROM\b`), "DELETE est destructif"},
		{regexp.MustCompile(`\bCONCURRENTLY\b`), "CONCURRENTLY est impossible dans la transaction du runner"},
		{regexp.MustCompile(`\bSET\s+NOT\s+NULL\b`), "SET NOT NULL est une étape CONTRACT"},
		{regexp.MustCompile(`\bALTER\s+COLUMN\b[^,;]*\bTYPE\b`), "changer le type d'une colonne réécrit la table (CONTRACT)"},
		{regexp.MustCompile(`\bADD\s+CONSTRAINT\b`), "ADD CONSTRAINT n'est pas idempotent (utiliser CREATE UNIQUE INDEX IF NOT EXISTS ou une contrainte dans CREATE TABLE)"},
	}
	reCreateTable   = regexp.MustCompile(`\bCREATE\s+TABLE\b`)
	reCreateTableOK = regexp.MustCompile(`\bCREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\b`)
	reCreateIndex   = regexp.MustCompile(`\bCREATE\s+(UNIQUE\s+)?INDEX\b`)
	reCreateIndexOK = regexp.MustCompile(`\bCREATE\s+(UNIQUE\s+)?INDEX\s+IF\s+NOT\s+EXISTS\b`)
	reAddColumn     = regexp.MustCompile(`\bADD\s+COLUMN\b`)
	reAddColumnOK   = regexp.MustCompile(`\bADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\b`)
	reAlterTable    = regexp.MustCompile(`^\s*ALTER\s+TABLE\b`)
	reNotNull       = regexp.MustCompile(`\bNOT\s+NULL\b`)
	reDefault       = regexp.MustCompile(`\bDEFAULT\b`)
	reInsert        = regexp.MustCompile(`^\s*INSERT\b`)
	reOnConflict    = regexp.MustCompile(`\bON\s+CONFLICT\b`)
)

// CheckExpandOnly refuse toute instruction qui n'est pas additive ET idempotente. Elle ne remplace
// pas la revue : une fonction Go (SystemFunc/WorkspaceFunc) n'est pas analysable.
func CheckExpandOnly(stmt string) error {
	s := reBlockComment.ReplaceAllString(stmt, " ")
	s = reLineComment.ReplaceAllString(s, " ")
	s = strings.ToUpper(reSpaces.ReplaceAllString(strings.TrimSpace(s), " "))
	if s == "" {
		return fmt.Errorf("instruction vide")
	}
	for _, b := range reBanned {
		if b.re.MatchString(s) {
			return fmt.Errorf("%s : %q", b.msg, abbreviate(stmt))
		}
	}
	if len(reCreateTable.FindAllString(s, -1)) != len(reCreateTableOK.FindAllString(s, -1)) {
		return fmt.Errorf("CREATE TABLE sans IF NOT EXISTS : %q", abbreviate(stmt))
	}
	if len(reCreateIndex.FindAllString(s, -1)) != len(reCreateIndexOK.FindAllString(s, -1)) {
		return fmt.Errorf("CREATE INDEX sans IF NOT EXISTS : %q", abbreviate(stmt))
	}
	if len(reAddColumn.FindAllString(s, -1)) != len(reAddColumnOK.FindAllString(s, -1)) {
		return fmt.Errorf("ADD COLUMN sans IF NOT EXISTS : %q", abbreviate(stmt))
	}
	if reAlterTable.MatchString(s) && reNotNull.MatchString(s) && !reDefault.MatchString(s) {
		return fmt.Errorf("colonne NOT NULL ajoutée sans DEFAULT (échoue sur une table peuplée) : %q", abbreviate(stmt))
	}
	if reInsert.MatchString(s) && !reOnConflict.MatchString(s) {
		return fmt.Errorf("INSERT sans ON CONFLICT n'est pas idempotent : %q", abbreviate(stmt))
	}
	return nil
}

func abbreviate(s string) string {
	s = reSpaces.ReplaceAllString(strings.TrimSpace(s), " ")
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}
