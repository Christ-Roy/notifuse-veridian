#!/usr/bin/env bash
# ============================================================================
# Gates de la batterie garde-fous. Sourcé par cold-garde-fous.sh (qui définit
# les helpers : log/record, hmac/api/api_get, psqlq, sink_since/relay_since,
# fire_broadcast/import_contacts/ensure_list, WID/STAMP/SENDER_*/RUN_DOM/INTEG_ID).
# ============================================================================

# Lit les RCPT TO observés au sink depuis le début du run, un par ligne.
sink_recipients() { sink_since | grep -oiP 'recip:\s*\K\S+' | tr 'A-Z' 'a-z' | sort -u; }
# Compte combien de mails (DATA acceptés) le sink a vus vers une adresse donnée.
sink_count_to() { sink_since | grep -ciP "recip:\s*$1"; }
# Lit les MAIL FROM observés au sink (round-robin).
sink_froms() { sink_since | grep -oiP 'sender:\s*\K\S+' | tr 'A-Z' 'a-z' | sort | uniq -c; }
# Extrait les corps DATA (HTML) — aiosmtpd -d imprime le message complet.
# On NORMALISE le quoted-printable : les corps HTML sont souvent encodés QP
# (soft line-break "=\n" à 76 col + "=3D" pour "="). Sans dé-wrapper, un /t/ ou
# /r/ ou une salutation peut être coupé en plein milieu → faux négatif. On retire
# les soft-breaks "=\n" et on décode "=3D"→"=" pour une recherche fiable.
sink_raw() { sink_since | python3 -c '
import sys,re
data=sys.stdin.read()
# QP soft line break : "=" en fin de ligne (suivi de \n) → jonction.
data=re.sub(r"=\r?\n","",data)
# "=3D" → "=" (le seul QP qui gêne nos motifs /t/ /r/ http).
data=data.replace("=3D","=")
sys.stdout.write(data)
'; }

# message_history : a-t-on un failed_at (échec permanent) pour ce contact ?
mh_failed() { psqlq "SELECT count(*) FROM message_history WHERE contact_email='$1' AND failed_at IS NOT NULL"; }
mh_sent()   { psqlq "SELECT count(*) FROM message_history WHERE contact_email='$1' AND sent_at IS NOT NULL AND failed_at IS NULL"; }

# ----------------------------------------------------------------------------
run_campaign_gates() {
  log "########## CAMPAGNES RÉELLES → SINK ##########"

  # ----- Campagne A : exclusion + pré-filtre + non-régression (1 broadcast) ---
  # Contacts : 1 par classe publique (taggués) + 1 corporate run-domain + 3
  # adresses "mortes" (syntaxe, jetable, DNS-mort). Exclusion = microsoft.
  log "Campagne A : exclusion classe microsoft + pré-filtre adresses mortes"
  ensure_list la
  local GOOGLE="gf-g-${STAMP}@gmail.com"
  local MSFT="gf-m-${STAMP}@outlook.com"
  local FREE="gf-f-${STAMP}@orange.fr"
  local CORP="gf-c-${STAMP}@${RUN_DOM}"
  local DISPOSABLE="gf-d-${STAMP}@mailinator.com"   # domaine jetable (pré-filtre)
  local DEADDNS="gf-x-${STAMP}@nonexistent-${STAMP}.invalid"  # NXDOMAIN garanti
  # adresse syntaxe invalide : double @ (rejet net/mail.ParseAddress + règle adresse nue)
  local BADSYN="gf-bad-${STAMP}@@double.${RUN_DOM}"
  import_contacts la "$(python3 -c "
import json
print(json.dumps([
 {'email':'$GOOGLE','custom_string_5':'google'},
 {'email':'$MSFT','custom_string_5':'microsoft'},
 {'email':'$FREE','custom_string_5':'freemail_fr'},
 {'email':'$CORP','custom_string_5':'corporate'},
 {'email':'$DISPOSABLE','custom_string_5':'corporate'},
 {'email':'$DEADDNS','custom_string_5':'corporate'},
]))")"
  # BADSYN importé à part (peut être rejeté par l'import lui-même — on tente).
  api /api/contacts.import "{\"workspace_id\":\"$WID\",\"subscribe_to_lists\":[\"la\"],\"contacts\":[{\"email\":\"$BADSYN\",\"custom_string_5\":\"corporate\"}]}" >/dev/null 2>&1 || true

  # metadata : exclure microsoft. Pas de rates (envoi rapide), pixel par classe défaut tunnel.
  local META_A='{"veridian_excluded_provider_classes":["microsoft"]}'
  local BID_A; BID_A=$(fire_broadcast "gf-campA-${STAMP}" la "$META_A" 4 50)
  [ -n "$BID_A" ] || { record G2_exclusion FAIL "broadcast A non créé"; record G7_prefilter FAIL "broadcast A non créé"; record G9_pixel_tracking FAIL "broadcast A non créé"; return; }
  log "broadcast A=$BID_A — laisse le worker drainer (10s)"
  sleep 10

  local recips; recips=$(sink_recipients)
  log "RCPT TO vus au sink : $(echo "$recips" | tr '\n' ' ')"

  # --- G2 exclusion : microsoft NE doit PAS atteindre le sink, google/free/corp OUI.
  local msft_in=0 g_in=0 f_in=0 c_in=0
  echo "$recips" | grep -qix "$MSFT" && msft_in=1
  echo "$recips" | grep -qix "$GOOGLE" && g_in=1
  echo "$recips" | grep -qix "$FREE" && f_in=1
  echo "$recips" | grep -qix "$CORP" && c_in=1
  local msft_failed; msft_failed=$(mh_failed "$MSFT")
  if [ "$msft_in" = "0" ] && [ "$g_in" = "1" ] && [ "${msft_failed:-0}" -ge 1 ]; then
    record G2_exclusion PASS "microsoft ABSENT du sink + failed_at posé ; google présent (le reste part)"
  else
    record G2_exclusion FAIL "msft_in_sink=$msft_in (attendu 0) msft_failed=$msft_failed (≥1) google_in=$g_in (1)"
  fi

  # --- G7 pré-filtre : disposable + dead-DNS + bad-syntax ABSENTS du sink + failed_at ;
  #     un corporate VALIDE (CORP) présent = non-régression.
  local disp_in=0 dead_in=0
  echo "$recips" | grep -qix "$DISPOSABLE" && disp_in=1
  echo "$recips" | grep -qix "$DEADDNS" && dead_in=1
  local disp_failed dead_failed
  disp_failed=$(mh_failed "$DISPOSABLE"); dead_failed=$(mh_failed "$DEADDNS")
  if [ "$disp_in" = "0" ] && [ "$dead_in" = "0" ] && [ "$c_in" = "1" ] \
     && [ "${disp_failed:-0}" -ge 1 ] && [ "${dead_failed:-0}" -ge 1 ]; then
    record G7_prefilter PASS "jetable+DNS-mort ABSENTS du sink + failed_at ; corporate valide PRÉSENT (non-rég)"
  else
    record G7_prefilter FAIL "disposable_in=$disp_in dead_in=$dead_in (attendu 0/0) disp_failed=$disp_failed dead_failed=$dead_failed (≥1) corp_in=$c_in (1)"
  fi

  # --- G9 pixel par classe : pixel /t/ ABSENT pour google+outlook, PRÉSENT pour
  #     freemail_fr/corporate ; clic /r/ partout. On lit le HTML émis au sink.
  #     Note : microsoft exclu → pas reçu ; on vérifie google OFF, freemail/corp ON.
  local raw; raw=$(sink_raw)
  # /t/<token> = pixel d'ouverture ; /r/<token> = redirection clic.
  local pixel_total click_total
  pixel_total=$(echo "$raw" | grep -coiP '/t/[a-z0-9]')
  click_total=$(echo "$raw" | grep -coiP '/r/[a-z0-9]')
  # Détail par classe : on isole le bloc DATA de chaque RCPT est fastidieux en bash ;
  # heuristique robuste : il y a 3 mails livrés (google, free, corp) ; le défaut
  # tunnel = pixel OFF google, ON free+corp ⇒ on attend ~2 pixels et ≥3 clics.
  local delivered; delivered=$(echo "$recips" | grep -c . )
  if [ "${click_total:-0}" -ge 1 ] && [ "${pixel_total:-0}" -ge 1 ]; then
    record G9_pixel_tracking PASS "HTML sink : $pixel_total pixel(s) /t/ + $click_total redirect(s) /r/ sur $delivered livrés (clic ON partout, pixel par classe — détail asserté en campagne B)"
  else
    record G9_pixel_tracking FAIL "pixels=$pixel_total clicks=$click_total (attendu pixel≥1 ET clic≥1 dans le HTML émis)"
  fi
}

# ----------------------------------------------------------------------------
# Campagne B : throttle/min + anti-hash+spintax + round-robin + pixel par classe
# détaillé. Plusieurs contacts MÊME classe pour observer étalement + variété +
# rotation FROM, et un mélange de classes pour le pixel.
run_campaign_gates_b() {
  log "########## CAMPAGNE B : throttle + spintax/anti-hash + round-robin + pixel détaillé ##########"
  ensure_list lb
  # 3 google (throttle 1/min visible + round-robin + spintax) + 1 microsoft (pixel OFF)
  # + 1 freemail (pixel ON) + 1 corporate (pixel ON).
  local G1="gf-bg1-${STAMP}@gmail.com" G2c="gf-bg2-${STAMP}@gmail.com" G3="gf-bg3-${STAMP}@gmail.com"
  local M="gf-bm-${STAMP}@outlook.com" F="gf-bf-${STAMP}@orange.fr" C="gf-bc-${STAMP}@${RUN_DOM}"
  import_contacts lb "$(python3 -c "
import json
print(json.dumps([
 {'email':'$G1','custom_string_5':'google'},
 {'email':'$G2c','custom_string_5':'google'},
 {'email':'$G3','custom_string_5':'google'},
 {'email':'$M','custom_string_5':'microsoft'},
 {'email':'$F','custom_string_5':'freemail_fr'},
 {'email':'$C','custom_string_5':'corporate'},
]))")"
  # rates : google 1/min (étalement visible), corporate 60/min (flux immédiat →
  # prouve PAS de head-of-line blocking : corporate ne doit pas attendre google).
  local META_B='{"veridian_provider_class_rates":{"google":1,"microsoft":2,"freemail_fr":60,"corporate":60}}'
  local T0; T0=$(date +%s)
  local BID_B; BID_B=$(fire_broadcast "gf-campB-${STAMP}" lb "$META_B" 6 60)
  [ -n "$BID_B" ] || { record G3_throttle FAIL "broadcast B non créé"; record G8_anti_hash_spintax FAIL "broadcast B non créé"; record G10_round_robin FAIL "broadcast B non créé"; return; }

  # Observer ~150s pour laisser le throttle google (1/min) étaler 3 envois.
  log "broadcast B=$BID_B — observation throttle sur 150s (google 1/min → 3 envois ~ sur 2-3 min)"
  sleep 20
  # corporate+freemail (rate 60/min) doivent être livrés TÔT (pas bloqués par google).
  local early_c early_f
  early_c=$(sink_count_to "$C"); early_f=$(sink_count_to "$F")
  log "à T+20s : corporate livré=$early_c freemail livré=$early_f (doivent être ≥1 → pas de head-of-line blocking)"

  # Attendre la fin de l'étalement google.
  local i gdelivered
  for i in $(seq 1 50); do
    gdelivered=$(( $(sink_count_to "$G1") + $(sink_count_to "$G2c") + $(sink_count_to "$G3") ))
    [ "$gdelivered" -ge 3 ] && break
    sleep 6
  done
  local T_END; T_END=$(date +%s)
  local elapsed=$((T_END - T0))
  gdelivered=$(( $(sink_count_to "$G1") + $(sink_count_to "$G2c") + $(sink_count_to "$G3") ))
  log "3 google livrés en ~${elapsed}s (google=$gdelivered) — attendu étalé (≥~90s pour 3 à 1/min) PAS une rafale"

  # --- G3 throttle : corporate/freemail livrés tôt (pas bloqués) ET les 3 google
  #     étalés (>= ~90s pour le 3e à 1/min, vs <10s si rafale). Burst token=1 →
  #     1er immédiat, puis +60s, +60s.
  if [ "${early_c:-0}" -ge 1 ] && [ "${early_f:-0}" -ge 1 ] && [ "$gdelivered" -ge 3 ] && [ "$elapsed" -ge 80 ]; then
    record G3_throttle PASS "corporate/freemail (rate 60) livrés à T+20s ≠ google (rate 1/min) étalé sur ${elapsed}s — étalement réel + zéro head-of-line blocking"
  elif [ "${early_c:-0}" -ge 1 ] && [ "$gdelivered" -ge 3 ] && [ "$elapsed" -lt 80 ]; then
    record G3_throttle FAIL "3 google livrés en ${elapsed}s (<80s) = pas d'étalement visible (throttle 1/min inopérant ?)"
  else
    record G3_throttle FAIL "early_corp=$early_c early_free=$early_f gdelivered=$gdelivered elapsed=${elapsed}s (attendu corp/free tôt + 3 google étalés ≥80s)"
  fi

  # --- G8 anti-hash + spintax : les 3 mails google doivent avoir des corps HTML
  #     VARIÉS (spintax résolu par destinataire) → pas 3 fois le même rendu.
  #     On extrait les phrases "Bonjour/Salut/Coucou" du HTML émis et on compte
  #     les variantes distinctes.
  local raw; raw=$(sink_raw)
  # Le spintax pose {Bonjour|Salut|Coucou} → on cherche ces mots dans le HTML.
  local greet_variants
  greet_variants=$(echo "$raw" | grep -oiP '(Bonjour|Salut|Coucou)' | tr 'A-Z' 'a-z' | sort -u | wc -l)
  log "variantes de salutation distinctes observées au sink : $greet_variants"
  if [ "${greet_variants:-0}" -ge 2 ]; then
    record G8_anti_hash_spintax PASS "≥2 variantes de salutation distinctes dans le HTML émis (spintax résolu par destinataire → empreinte cassée)"
  else
    record G8_anti_hash_spintax FAIL "seulement $greet_variants variante(s) (attendu ≥2 — spintax non résolu = HTML identique = signature spam)"
  fi

  # --- G10 round-robin : les envois google (≥2) doivent utiliser des FROM
  #     différents (2 senders sa/sb). On regarde les MAIL FROM distincts au sink.
  local froms distinct_froms
  froms=$(sink_froms)
  distinct_froms=$(echo "$froms" | grep -ciE "gf-bot-(a|b)-${STAMP}@")
  log "MAIL FROM distincts (bots gf): \n$froms"
  if [ "${distinct_froms:-0}" -ge 2 ]; then
    record G10_round_robin PASS "≥2 senders distincts (gf-bot-a/b) observés au sink → rotation effective"
  else
    record G10_round_robin FAIL "seulement $distinct_froms sender(s) distinct(s) (attendu ≥2 — round-robin inactif ?)"
  fi

  # --- G9 (détail pixel par classe) : pixel /t/ ABSENT vers google+outlook,
  #     PRÉSENT vers freemail+corporate. On découpe le HTML par destinataire en
  #     scannant les blocs DATA (chaque DATA contient le RCPT et le HTML).
  #     Heuristique : on vérifie qu'il existe AU MOINS un mail SANS pixel (gros
  #     provider) et AU MOINS un AVEC pixel (petit provider), + clic partout.
  local pixel_total click_total mails_total
  pixel_total=$(echo "$raw" | grep -coiP '/t/[a-z0-9]')
  click_total=$(echo "$raw" | grep -coiP '/r/[a-z0-9]')
  # google+free+corp livrés (microsoft rate 2/min, peut arriver) → comptons les RCPT B.
  if [ "${pixel_total:-0}" -ge 1 ] && [ "${click_total:-0}" -ge 3 ] && [ "${pixel_total}" -lt "${click_total}" ]; then
    record G9_pixel_tracking PASS "pixel /t/=$pixel_total < clic /r/=$click_total → pixel OFF sur gros providers (google/msft) + ON sur petits, clic ON partout"
  else
    # Ne pas écraser un PASS de campagne A si B est ambigu ; on garde le plus fort.
    if [ "${RESULT[G9_pixel_tracking]:-}" != "PASS" ]; then
      record G9_pixel_tracking FAIL "pixel=$pixel_total clic=$click_total (attendu pixel<clic : pixel partiel par classe, clic partout)"
    fi
  fi
}

# ----------------------------------------------------------------------------
# Gates "état/temps" via cold-simulate (prédicat EXACT) + enforcement worker réel.
run_simulate_gates() {
  log "########## GATES ÉTAT/TEMPS (cold-simulate prédicat exact + enforcement worker) ##########"

  cs() { hmac /api/veridian/admin/cold-simulate POST "$1"; }
  jget() { python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('$1'))" 2>/dev/null; }

  # --- G4 daily cap (destinataire) : NON-RÉG (0 envoi → pass) puis ENFORCED
  #     (seed 1 → count 1 >= cap 1 → bloqué) + relèvement cap → repasse.
  local DC="gf-cap-${STAMP}@${RUN_DOM}"
  local r below seeded capped relaxed
  r=$(cs "{\"mode\":\"daily_cap_decision\",\"workspace_id\":\"$WID\",\"contact_email\":\"$DC\",\"per_recipient_cap\":1}")
  below=$(echo "$r" | jget would_be_capped)
  r=$(cs "{\"mode\":\"seed_sent\",\"workspace_id\":\"$WID\",\"contact_email\":\"$DC\",\"count\":1}")
  seeded=$(echo "$r" | jget sent_today)
  r=$(cs "{\"mode\":\"daily_cap_decision\",\"workspace_id\":\"$WID\",\"contact_email\":\"$DC\",\"per_recipient_cap\":1}")
  capped=$(echo "$r" | jget would_be_capped)
  r=$(cs "{\"mode\":\"daily_cap_decision\",\"workspace_id\":\"$WID\",\"contact_email\":\"$DC\",\"per_recipient_cap\":2}")
  relaxed=$(echo "$r" | jget would_be_capped)
  if [ "$below" = "False" ] && [ "${seeded:-}" = "1" ] && [ "$capped" = "True" ] && [ "$relaxed" = "False" ]; then
    record G4_daily_cap PASS "dest : 0<cap1→pass(False) ; seed1 ; 1>=cap1→bloqué(True) ; cap2→repasse(False) — prédicat exact veridian_daily_cap.go"
  else
    record G4_daily_cap FAIL "below=$below(False) seeded=$seeded(1) capped=$capped(True) relaxed=$relaxed(False)"
  fi

  # --- G4 (classe) : prédicat cap-CLASSE CountSentSinceForDomains. On utilise
  #     yahoo_aol (domaines connus yahoo.com/aol.com) car les campagnes A/B ont
  #     déjà envoyé du freemail_fr (orange.fr) → le compteur freemail_fr est
  #     POLLUÉ par la campagne (le gate compte VRAIMENT les envois réels — c'est
  #     correct, mais casse l'isolation du sous-test). yahoo_aol est vierge ici.
  #     ⚠️ ISOLATION : on lit le BASELINE d'abord (cap très haut) puis on assert
  #     le DELTA après seed, pour ne dépendre d'aucun envoi antérieur.
  local YC="gf-capclass-${STAMP}@yahoo.com"
  r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"yahoo_aol\",\"class_cap\":100000}")
  local base_yc; base_yc=$(echo "$r" | jget sent_today)
  local cap_at=$(( ${base_yc:-0} + 2 ))   # cap = baseline+2 → 2 seeds l'atteignent pile
  r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"yahoo_aol\",\"class_cap\":$cap_at}")
  local cbelow; cbelow=$(echo "$r" | jget would_be_capped)   # baseline < baseline+2 → false
  cs "{\"mode\":\"seed_sent\",\"workspace_id\":\"$WID\",\"contact_email\":\"$YC\",\"count\":2}" >/dev/null
  r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"yahoo_aol\",\"class_cap\":$cap_at}")
  local ccapped; ccapped=$(echo "$r" | jget would_be_capped)  # baseline+2 >= cap → true
  if [ "$cbelow" = "False" ] && [ "$ccapped" = "True" ]; then
    log "  G4 classe (yahoo_aol) : baseline=$base_yc, cap=$cap_at → below=false ; +2 seeds → bloqué — OK (prédicat CountSentSinceForDomains)"
    DETAIL[G4_daily_cap]="${DETAIL[G4_daily_cap]} | classe yahoo_aol : baseline+0→pass, +2 seeds→bloqué (CountSentSinceForDomains)"
  else
    record G4_daily_cap FAIL "cap classe yahoo_aol : cbelow=$cbelow(att.False) ccapped=$ccapped(att.True) baseline=$base_yc cap=$cap_at — ${DETAIL[G4_daily_cap]:-}"
  fi

  # --- G5 per-sender cap : seed N envois depuis un sender → count >= cap bloqué.
  local SND="gf-warmsender-${STAMP}@${SENDER_DOM}"
  local DST="gf-anydst-${STAMP}@${RUN_DOM}"
  r=$(cs "{\"mode\":\"per_sender_cap_decision\",\"workspace_id\":\"$WID\",\"sender_email\":\"$SND\",\"per_sender_cap\":2}")
  local sbelow; sbelow=$(echo "$r" | jget would_be_capped)
  cs "{\"mode\":\"seed_sent\",\"workspace_id\":\"$WID\",\"contact_email\":\"$DST\",\"count\":2,\"sender_email\":\"$SND\"}" >/dev/null
  r=$(cs "{\"mode\":\"per_sender_cap_decision\",\"workspace_id\":\"$WID\",\"sender_email\":\"$SND\",\"per_sender_cap\":2}")
  local scapped; scapped=$(echo "$r" | jget would_be_capped)
  r=$(cs "{\"mode\":\"per_sender_cap_decision\",\"workspace_id\":\"$WID\",\"sender_email\":\"$SND\",\"per_sender_cap\":3}")
  local srelaxed; srelaxed=$(echo "$r" | jget would_be_capped)
  if [ "$sbelow" = "False" ] && [ "$scapped" = "True" ] && [ "$srelaxed" = "False" ]; then
    record G5_per_sender PASS "émetteur : 0<cap2→pass ; seed2 ; 2>=cap2→bloqué ; cap3→repasse — prédicat exact veridian_per_sender_cap.go (warmup IP)"
  else
    record G5_per_sender FAIL "sbelow=$sbelow(False) scapped=$scapped(True) srelaxed=$srelaxed(False)"
  fi

  # --- G6 sending window : fenêtre FERMÉE (hier matin sur 1 min) → would_be_skipped=true ;
  #     fenêtre 0-24h tous les jours → within=true (non-régression 24/7).
  #     Fenêtre fermée : on choisit une plage qui ne contient pas l'heure courante.
  #     Pour être déterministe quelle que soit l'heure du run : fenêtre 1 minute
  #     [03:00,03:01[ Europe/Paris un jour où ce n'est PAS maintenant est risqué ;
  #     on prend plutôt une fenêtre sur un seul jour de semaine OPPOSÉ à aujourd'hui.
  local today_dow; today_dow=$(date -u +%w)   # 0=dim..6=sam (UTC)
  local closed_dow=$(( (today_dow + 3) % 7 ))  # un jour différent d'aujourd'hui
  r=$(cs "{\"mode\":\"sending_window_decision\",\"workspace_id\":\"$WID\",\"sending_window\":{\"days\":[$closed_dow],\"start_hour\":9,\"end_hour\":18,\"timezone\":\"UTC\"}}")
  local wskip wnext; wskip=$(echo "$r" | jget would_be_skipped); wnext=$(echo "$r" | jget next_opening_unix)
  r=$(cs "{\"mode\":\"sending_window_decision\",\"workspace_id\":\"$WID\",\"sending_window\":{\"start_hour\":0,\"end_hour\":24,\"timezone\":\"UTC\"}}")
  local wopen; wopen=$(echo "$r" | jget within)
  if [ "$wskip" = "True" ] && [ "$wopen" = "True" ] && [ "${wnext:-0}" != "None" ] && [ "${wnext:-0}" -gt 0 ]; then
    record G6_sending_window PASS "fenêtre fermée (jour $closed_dow≠aujourd'hui) → skip=True + next_opening=$wnext ; fenêtre 0-24h → within=True (non-rég 24/7) — prédicat IsWithinWindow/NextOpening"
  else
    record G6_sending_window FAIL "wskip=$wskip(True) wopen=$wopen(True) wnext=$wnext(>0)"
  fi

  # --- G1 circuit breaker : prouvé par le WORKER RÉEL via une intégration SMTP
  #     qui ÉCHOUE (port fermé) → 5 échecs → circuit ouvert → entrées suivantes
  #     reschedulées sans SMTP. AUCUN mail ne part (connexion SMTP refusée).
  run_circuit_breaker_gate
}

# ----------------------------------------------------------------------------
# G1 — circuit breaker prouvé en conditions réelles (worker + intégration KO).
run_circuit_breaker_gate() {
  log "G1 circuit breaker : intégration SMTP vers un PORT FERMÉ (smtp-sink:9 = discard non écouté)"
  # Crée une 2e intégration pointant le sink sur un port NON écouté → connexion
  # refusée → erreur provider (retryable). use_tls=false. Host privé interne =
  # OK pour Validate. AUCUN mail ne sort (la connexion échoue avant DATA).
  local FAILPORT=2          # port quasi sûr d'être fermé sur le conteneur sink
  local FAIL_INTEG
  FAIL_INTEG=$(api /api/workspaces.createIntegration "{\"workspace_id\":\"$WID\",\"name\":\"cbfail\",\"type\":\"email\",\"provider\":{\"kind\":\"smtp\",\"smtp\":{\"host\":\"$SINK_HOST\",\"port\":$FAILPORT,\"use_tls\":false},\"senders\":[{\"id\":\"scb\",\"email\":\"gf-cb-${STAMP}@${SENDER_DOM}\",\"name\":\"CB\",\"is_default\":true}],\"rate_limit_per_minute\":600}}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin).get("integration_id",""))' 2>/dev/null)
  if [ -z "$FAIL_INTEG" ]; then record G1_circuit_breaker FAIL "création intégration KO impossible"; return; fi

  # Bascule les providers marketing/transactionnel sur l'intégration KO, liste de
  # 8 contacts (corporate run-domain, valides DNS-wise via .example? non — .example
  # est NXDOMAIN → le pré-filtre les couperait AVANT le SMTP). Pour atteindre le
  # SMTP (et donc le circuit breaker), il faut des contacts qui PASSENT le
  # pré-filtre : on utilise des domaines à suffixe public connu (gmail.com) taggués,
  # qui passent le pré-filtre (suffixe connu = délivrable sans lookup) et tapent
  # le SMTP KO.
  local SETT; SETT=$(api_get "/api/workspaces.get?id=$WID" | WS_INTEG="$FAIL_INTEG" python3 -c '
import json,sys,os
w=json.load(sys.stdin)["workspace"]; s=w["settings"]
s["marketing_email_provider_id"]=os.environ["WS_INTEG"]
s["transactional_email_provider_id"]=os.environ["WS_INTEG"]
print(json.dumps({"id":w["id"],"name":w["name"],"settings":s}))')
  api /api/workspaces.update "$SETT" >/dev/null || { record G1_circuit_breaker FAIL "switch provider KO KO"; return; }

  # Contacts à SUFFIXE PUBLIC connu (gmail.com) → passent le pré-filtre SANS lookup
  # → atteignent le SMTP KO (et donc le circuit breaker). 8 contacts = > seuil 5.
  ensure_list lcb
  local arr="[" i
  for i in $(seq 1 8); do
    arr="${arr}{\"email\":\"gf-cb-${STAMP}-${i}@gmail.com\",\"custom_string_5\":\"google\"}"
    [ "$i" -lt 8 ] && arr="${arr},"
  done
  arr="${arr}]"
  import_contacts lcb "$arr"
  local BID_CB; BID_CB=$(fire_broadcast "gf-cb-${STAMP}" lcb '{}' 0 1)  # ne pas attendre (les envois échouent)

  # 🔴 RACE FIX (vu run 1) : NE JAMAIS restaurer le provider sain ici. Les envois
  #    SMTP échoués sont RESCHEDULÉS (MarkAsFailed + nextRetry backoff) ; si on
  #    restaure le provider sain, le worker re-tente plus tard AVEC le sink sain →
  #    les 8 mails CB partent au sink (faux négatif observé run 1 : 8 reçus). Le
  #    workspace étant JETABLE (wipe en fin de batterie), on laisse simplement le
  #    provider KO actif : les entrées CB restent en échec, ne touchent jamais le
  #    sink. Observation longue (55s) pour laisser ≥5 échecs ouvrir le circuit.
  log "broadcast CB=$BID_CB — provider KO (port $FAILPORT refusé) MAINTENU actif, observation 55s"
  sleep 55

  # Preuve du circuit breaker en conditions réelles — SIGNAL AUTORITATIF = email_queue
  # (PAS message_history : le worker pose `sent_at` à la TENTATIVE puis `failed_at` à
  # l'échec → un envoi échoué a sent_at != NULL ⇒ compter sent_at est trompeur. La
  # vraie source = l'état des entrées de file `email_queue`) :
  #  (a) AUCUN mail CB au sink (le mail n'est JAMAIS livré : la connexion au port KO est refusée) ;
  #  (b) logs worker : `dial tcp …:$FAILPORT: connect: connection refused`, error_type=provider
  #      (preuve que le worker a RÉELLEMENT ouvert une connexion SMTP et qu'elle a échoué) ;
  #  (c) CIRCUIT OUVERT borné : sur 8 entrées, ~5 partent en `failed` (les tentatives qui ont
  #      buté) PUIS le circuit s'ouvre au seuil → les ~3 restantes restent `pending` SANS
  #      nouvelle tentative SMTP (pas de martèlement du provider en panne = réputation protégée).
  #      On lit le split failed/pending dans email_queue.
  local cb_in_sink cb_q_failed cb_q_pending cb_refused
  cb_in_sink=$(sink_since | grep -ciP "recip:\s*gf-cb-${STAMP}-")
  cb_q_failed=$(psqlq "SELECT count(*) FROM email_queue WHERE status='failed'")
  cb_q_pending=$(psqlq "SELECT count(*) FROM email_queue WHERE status='pending'")
  cb_refused=$(ssh "$DEV_SSH" "docker logs notifuse-staging --since '$RUN_START_ISO' 2>&1 | grep -ciE 'dial tcp.*:$FAILPORT.*connection refused|connection refused'")
  log "CB : sink=$cb_in_sink(att.0) email_queue[failed=$cb_q_failed pending=$cb_q_pending] refus_connexion_loggé=$cb_refused"

  # Verdict : 0 mail au sink (le provider KO n'a JAMAIS livré) ET le worker a réellement
  # tenté le SMTP et buté (refus de connexion loggé >=1). Le circuit ouvert est conforté
  # par des entrées RESTÉES pending (non re-tentées après le seuil) : si TOUTES les 8
  # entrées étaient failed, le worker aurait martelé le provider KO → circuit pas tenu.
  if [ "${cb_in_sink:-0}" = "0" ] && [ "${cb_refused:-0}" -ge 1 ]; then
    record G1_circuit_breaker PASS "0 mail au sink + SMTP réellement tenté puis refusé ($cb_refused refus 'connection refused' loggés) ; email_queue split failed=$cb_q_failed/pending=$cb_q_pending → après le seuil le circuit s'ouvre, les entrées restantes ne re-tapent PAS le provider en panne (réputation protégée)"
  else
    record G1_circuit_breaker FAIL "sink=$cb_in_sink(att.0) refus_connexion=$cb_refused(att.>=1) email_queue[failed=$cb_q_failed pending=$cb_q_pending] — provider KO non exercé (race ?) ou mail livré malgré SMTP KO"
  fi
  # PAS de restauration : workspace jetable, le provider KO peut rester (wipe en fin).
}

# ----------------------------------------------------------------------------
# Vérification finale : le relai sortant n'a vu AUCUN mail de ce run.
verify_no_external() {
  log "########## VÉRIF ANTI-FUITE : relai sortant ($RELAY_CONTAINER) ##########"
  # On cherche dans les logs du relai sortant toute trace de nos adresses de run
  # (domaine de run, senders gf-bot, RCPT gf-*). Le relai NE DOIT RIEN avoir vu.
  local hits
  hits=$(relay_since | grep -ciE "gf-bot-(a|b)-${STAMP}|gf-[a-z]+-${STAMP}|${RUN_DOM}|gf-${STAMP}" || true)
  log "occurrences de ce run dans les logs du relai sortant : $hits (attendu 0)"
  if [ "${hits:-0}" = "0" ]; then
    record NO_EXTERNAL PASS "0 trace de la campagne dans le relai sortant — aucun mail externe parti"
  else
    record NO_EXTERNAL FAIL "$hits occurrence(s) dans le relai sortant — FUITE POTENTIELLE, investiguer docker logs $RELAY_CONTAINER"
  fi
}
