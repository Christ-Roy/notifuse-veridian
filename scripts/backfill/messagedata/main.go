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
// identifiants et des compteurs, jamais un contenu. Codes de sortie :
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

func main() {
	dbDSN := flag.String("db", "", "DSN de la base workspace notifuse_ws_<id>")
	sysDSN := flag.String("system-db", "", "DSN de notifuse_system (lecture de la clé du workspace)")
	wsID := flag.String("workspace-id", "", "identifiant du workspace")
	batch := flag.Int("batch", 200, "taille de lot")
	apply := flag.Bool("apply", false, "écrire (sinon dry-run)")
	journalPath := flag.String("journal", "", "fichier journal (ids et compteurs seulement)")
	flag.Parse()

	globalKey := os.Getenv("SECRET_KEY")
	if *dbDSN == "" || *sysDSN == "" || *wsID == "" || globalKey == "" {
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
	var encKey sql.NullString
	err = sys.QueryRow(`SELECT settings->>'encrypted_secret_key' FROM workspaces WHERE id = $1`, *wsID).Scan(&encKey)
	if err != nil || !encKey.Valid || encKey.String == "" {
		logf("erreur: clé workspace introuvable (%v)", err)
		os.Exit(1)
	}
	wsKey, err := crypto.DecryptFromHexString(encKey.String, globalKey)
	if err != nil {
		logf("erreur: déchiffrement de la clé workspace: %v", err)
		os.Exit(1)
	}

	db, err := sql.Open("postgres", *dbDSN)
	if err != nil {
		logf("erreur base: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	var total, converted, failed int
	lastID := ""
	for {
		rows, err := db.Query(`
			SELECT id, message_data::text FROM message_history
			WHERE id > $1
			  AND jsonb_typeof(message_data->'data') = 'object'
			  AND message_data->'data' <> '{}'::jsonb
			  AND NOT (message_data->'data' ? '_encrypted')
			ORDER BY id LIMIT $2`, lastID, *batch)
		if err != nil {
			logf("erreur lecture: %v", err)
			os.Exit(1)
		}
		var lot []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.text); err != nil {
				rows.Close()
				logf("erreur scan: %v", err)
				os.Exit(1)
			}
			lot = append(lot, r)
		}
		rows.Close()
		if len(lot) == 0 {
			break
		}
		for _, r := range lot {
			total++
			lastID = r.id
			newText, err := encryptRow(r.text, wsKey)
			if err == nil {
				err = verifyRow(r.text, newText, wsKey)
			}
			if err != nil {
				failed++
				logf("ECHEC id=%s: %v", r.id, err)
				logf("bilan arret total=%d convertis=%d echecs=%d dernier_id=%s", total, converted, failed, r.id)
				os.Exit(2)
			}
			if *apply {
				res, err := db.Exec(`UPDATE message_history SET message_data = $1::jsonb
					WHERE id = $2 AND message_data::text = $3`, newText, r.id, r.text)
				if err != nil {
					logf("erreur update id=%s: %v", r.id, err)
					os.Exit(1)
				}
				if n, _ := res.RowsAffected(); n != 1 {
					// Ligne modifiée entre lecture et écriture : elle n'est pas écrasée.
					logf("ignore id=%s (modifiee pendant le lot)", r.id)
					continue
				}
			}
			converted++
		}
		logf("lot fini total=%d convertis=%d echecs=%d dernier_id=%s", total, converted, failed, lastID)
	}
	logf("bilan fin mode=%s total=%d convertis=%d echecs=%d", mode, total, converted, failed)
}
