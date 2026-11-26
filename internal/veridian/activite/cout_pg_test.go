package activite

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// Mesure du coût des déclencheurs sur message_history (VERIDIAN_ACTIVITE_MESURE=1, ignorée sinon) : 10 000 envois insérés puis ouverts,
// sans déclencheur, avec déclencheur sans abonnement du Hub, avec abonnement ; puis 10 000 envois d'une ligne chacun, chronométrés dans la base. Chaque scénario sur une base neuve, trois tours, le meilleur.
func TestMesureCoutDeclencheurs(t *testing.T) {
	if os.Getenv("VERIDIAN_ACTIVITE_MESURE") != "1" {
		t.Skip("mesure à la demande : VERIDIAN_ACTIVITE_MESURE=1")
	}
	run := func(name string, prep func(t *testing.T, db *dbT)) {
		best := [3]time.Duration{time.Hour, time.Hour, time.Hour}
		for i := 0; i < 3; i++ {
			db := testDB(t)
			prep(t, &dbT{db})
			t0 := time.Now()
			exec(t, db, `INSERT INTO message_history (id, contact_email, sent_at, veridian_sender_email) SELECT 'ws1_' || g, 'c' || g || '@x.fr', NOW(), 'h@a.fr' FROM generate_series(1, 10000) g`)
			ins := time.Since(t0)
			t0 = time.Now()
			exec(t, db, `UPDATE message_history SET opened_at = NOW()`)
			upd := time.Since(t0)
			// Comme en vrai : un envoi = une instruction d'une ligne. 10 000 instructions jouées dans la base (sans le réseau, qui
			// noierait l'écart), le temps lu côté serveur.
			var ms float64
			exec(t, db, `CREATE OR REPLACE FUNCTION mesure_un_par_un() RETURNS float8 AS $f$
				DECLARE t0 timestamptz := clock_timestamp();
				BEGIN
					FOR i IN 1..10000 LOOP
						INSERT INTO message_history (id, contact_email, sent_at, veridian_sender_email) VALUES ('ws1_u' || i, 'u@x.fr', NOW(), 'h@a.fr');
					END LOOP;
					RETURN extract(epoch FROM clock_timestamp() - t0) * 1000;
				END $f$ LANGUAGE plpgsql`)
			if err := db.QueryRow(`SELECT mesure_un_par_un()`).Scan(&ms); err != nil {
				t.Fatal(err)
			}
			one := time.Duration(ms * float64(time.Millisecond))
			best[0], best[1], best[2] = min(best[0], ins), min(best[1], upd), min(best[2], one)
		}
		t.Logf("%-28s insertion 10 000 : %6.1f ms   ouverture 10 000 : %6.1f ms   10 000 envois un par un : %6.1f ms", name,
			float64(best[0].Microseconds())/1000, float64(best[1].Microseconds())/1000, float64(best[2].Microseconds())/1000)
	}
	run("sans declencheur", func(t *testing.T, d *dbT) {
		exec(t, d.DB, `DROP TRIGGER veridian_activite_history ON message_history`)
	})
	run("declencheur, sans abonnement", func(t *testing.T, d *dbT) {})
	run("declencheur, abonnement", func(t *testing.T, d *dbT) { subscribe(t, d.DB, "https://hub.test/api/evenements/notifuse", testSecret) })
}

type dbT struct{ DB *sql.DB }
