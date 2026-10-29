// Backfill lot 0 : rechiffre message_history.message_data restées en clair
// (réécrites par l'ancien Update). Un workspace à la fois.
//
//	SECRET_KEY=... go run ./scripts/backfill/messagedata \
//	    -db 'postgres://user:pass@host:5432/notifuse_ws_<id>?sslmode=disable' \
//	    -system-db 'postgres://user:pass@host:5432/notifuse_system?sslmode=disable' \
//	    -workspace-id <id> [-batch 200] [-apply] [-journal chemin]
//
// Par défaut : DRY-RUN (aucune écriture). -apply écrit. Idempotent : seules les
// lignes sans clé _encrypted sont lues, chaque UPDATE garde la valeur lue
// (WHERE message_data::text = ancienne valeur). Le journal ne contient que des
// identifiants et des compteurs, jamais un contenu. Sauvegarde restaurable à
// vérifier AVANT -apply : docs/claude/75-l4-qualite-securite.md §Backfill. Codes de sortie :
// 0 = terminé sans erreur, 1 = erreur de configuration ou de base,
// 2 = incomplet (une ligne n'a pas passé l'aller-retour, arrêt).
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
	_ "github.com/lib/pq"
)

type row struct {
	id   string
	text string
}

// encryptRow reproduit encryptMessageData du dépôt (repository) : data sérialisée
// puis chiffrée, metadata conservée telle quelle.
func encryptRow(text string, wsKey string) (string, error) {
	var md struct {
		Data     map[string]interface{} `json:"data"`
		Metadata json.RawMessage        `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(text), &md); err != nil {
		return "", fmt.Errorf("json: %w", err)
	}
	plain, err := json.Marshal(md.Data)
	if err != nil {
		return "", err
	}
	enc, err := crypto.EncryptString(string(plain), wsKey)
	if err != nil {
		return "", err
	}
	out := map[string]interface{}{
		"data":     map[string]interface{}{"_encrypted": enc},
		"metadata": md.Metadata,
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// verifyRow déchiffre le résultat et compare à l'original. Aucun contenu n'est affiché.
func verifyRow(original, newText, wsKey string) error {
	var o, n struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal([]byte(original), &o); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(newText), &n); err != nil {
		return err
	}
	enc, ok := n.Data["_encrypted"].(string)
	if !ok {
		return errors.New("résultat sans _encrypted")
	}
	plain, err := crypto.DecryptFromHexString(enc, wsKey)
	if err != nil {
		return fmt.Errorf("déchiffrement: %w", err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal([]byte(plain), &back); err != nil {
		return err
	}
	if !reflect.DeepEqual(back, o.Data) {
		return errors.New("aller-retour différent de l'original")
	}
	return nil
}

// config regroupe ce dont backfill a besoin : testable sans processus ni flags.
type config struct {
	db    *sql.DB
	wsKey string
	batch int
	apply bool
	logf  func(format string, a ...interface{})
	// Crochets de test (nil en production) : chiffreur remplaçable, et appel entre
	// la lecture d'une ligne et son UPDATE.
	encrypt     func(text, wsKey string) (string, error)
	beforeWrite func(id string)
}

// bilan des lignes traitées.
type bilan struct {
	total, converted, failed, skipped int
	lastID                            string
}

// errIncomplet : une ligne n'a pas passé l'aller-retour, le traitement s'est arrêté
// sur elle (code de sortie 2). Toute autre erreur est une erreur de base (code 1).
var errIncomplet = errors.New("incomplet : une ligne n'a pas passé l'aller-retour")

// loadWorkspaceKey déchiffre la clé du workspace depuis notifuse_system avec SECRET_KEY.
func loadWorkspaceKey(sys *sql.DB, wsID, globalKey string) (string, error) {
	var encKey sql.NullString
	err := sys.QueryRow(`SELECT settings->>'encrypted_secret_key' FROM workspaces WHERE id = $1`, wsID).Scan(&encKey)
	if err != nil || !encKey.Valid || encKey.String == "" {
		return "", fmt.Errorf("clé workspace introuvable (%v)", err)
	}
	wsKey, err := crypto.DecryptFromHexString(encKey.String, globalKey)
	if err != nil {
		return "", fmt.Errorf("déchiffrement de la clé workspace: %w", err)
	}
	return wsKey, nil
}

// backfill rechiffre par lots les lignes en clair. Idempotent : une ligne déjà
// chiffrée ou vide n'est jamais lue.
func backfill(c config) (bilan, error) {
	var b bilan
	if c.encrypt == nil {
		c.encrypt = encryptRow
	}
	for {
		rows, err := c.db.Query(`
			SELECT id, message_data::text FROM message_history
			WHERE id > $1
			  AND jsonb_typeof(message_data->'data') = 'object'
			  AND message_data->'data' <> '{}'::jsonb
			  AND NOT (message_data->'data' ? '_encrypted')
			ORDER BY id LIMIT $2`, b.lastID, c.batch)
		if err != nil {
			return b, fmt.Errorf("lecture: %w", err)
		}
		var lot []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.text); err != nil {
				rows.Close()
				return b, fmt.Errorf("scan: %w", err)
			}
			lot = append(lot, r)
		}
		rows.Close()
		if len(lot) == 0 {
			return b, nil
		}
		for _, r := range lot {
			b.total++
			b.lastID = r.id
			newText, err := c.encrypt(r.text, c.wsKey)
			if err == nil {
				err = verifyRow(r.text, newText, c.wsKey)
			}
			if err != nil {
				b.failed++
				c.logf("ECHEC id=%s: %v", r.id, err)
				c.logf("bilan arret total=%d convertis=%d echecs=%d dernier_id=%s", b.total, b.converted, b.failed, r.id)
				return b, errIncomplet
			}
			if c.beforeWrite != nil {
				c.beforeWrite(r.id)
			}
			if c.apply {
				res, err := c.db.Exec(`UPDATE message_history SET message_data = $1::jsonb
					WHERE id = $2 AND message_data::text = $3`, newText, r.id, r.text)
				if err != nil {
					return b, fmt.Errorf("update id=%s: %w", r.id, err)
				}
				if n, _ := res.RowsAffected(); n != 1 {
					// Ligne modifiée entre lecture et écriture : elle n'est pas écrasée.
					b.skipped++
					c.logf("ignore id=%s (modifiee pendant le lot)", r.id)
					continue
				}
			}
			b.converted++
		}
		c.logf("lot fini total=%d convertis=%d echecs=%d ignores=%d dernier_id=%s", b.total, b.converted, b.failed, b.skipped, b.lastID)
	}
}

func main() {
	dbDSN := flag.String("db", "", "DSN de la base workspace notifuse_ws_<id>")
	sysDSN := flag.String("system-db", "", "DSN de notifuse_system (lecture de la clé du workspace)")
	wsID := flag.String("workspace-id", "", "identifiant du workspace")
	batch := flag.Int("batch", 200, "taille de lot")
	apply := flag.Bool("apply", false, "écrire (sinon dry-run)")
	journalPath := flag.String("journal", "", "fichier journal (ids et compteurs seulement)")
	flag.Parse()

	globalKey := os.Getenv("SECRET_KEY")
	if *dbDSN == "" || *sysDSN == "" || *wsID == "" || globalKey == "" || *batch < 1 {
		fmt.Fprintln(os.Stderr, "usage: SECRET_KEY=... -db -system-db -workspace-id [-batch] [-apply] [-journal]")
		os.Exit(1)
	}

	journal := os.Stdout
	if *journalPath != "" {
		f, err := os.OpenFile(*journalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "journal:", err)
			os.Exit(1)
		}
		defer f.Close()
		journal = f
	}
	logf := func(format string, a ...interface{}) {
		fmt.Fprintf(journal, time.Now().UTC().Format(time.RFC3339)+" "+format+"\n", a...)
	}
	mode := "dry-run"
	if *apply {
		mode = "apply"
	}
	logf("debut mode=%s workspace=%s batch=%d", mode, *wsID, *batch)

	sys, err := sql.Open("postgres", *sysDSN)
	if err != nil {
		logf("erreur systeme: %v", err)
		os.Exit(1)
	}
	defer sys.Close()
	wsKey, err := loadWorkspaceKey(sys, *wsID, globalKey)
	if err != nil {
		logf("erreur: %v", err)
		os.Exit(1)
	}

	db, err := sql.Open("postgres", *dbDSN)
	if err != nil {
		logf("erreur base: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	b, err := backfill(config{db: db, wsKey: wsKey, batch: *batch, apply: *apply, logf: logf})
	switch {
	case errors.Is(err, errIncomplet):
		os.Exit(2)
	case err != nil:
		logf("erreur: %v", err)
		os.Exit(1)
	}
	logf("bilan fin mode=%s total=%d convertis=%d echecs=%d ignores=%d", mode, b.total, b.converted, b.failed, b.skipped)
}
