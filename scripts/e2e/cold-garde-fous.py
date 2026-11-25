#!/usr/bin/env python3
"""Batterie « garde-fous anti-cramage de domaine », sur le VRAI worker de staging et par l'API seule (fiche 14).

Remplace cold-garde-fous.sh (25/11/2026) : ni base, ni journal de conteneur, ni ssh, ni jeton d'exec (VER-250). Les preuves viennent de ce que
l'application sait d'elle-même : messages.list (heure d'envoi, échec et sa raison), decisions.list (portes, expéditeur
retenu, empreinte du contenu, pixel), queue.explain, et cold-simulate (prédicat exact).

Aucun mail réel : le profil d'envoi pointe 127.0.0.1:1025 (puits sans relais, dans l'allocation de staging), relu avant tout envoi ; une autre adresse
arrête tout. Espace jetable gfcheck…, effacé à la fin.

Sortie : 0 toutes les portes tiennent · 1 une porte est en défaut · 2 non abouti (rien n'est affirmé). Une porte non lisible par l'API est dite, jamais verte.
Usage : cold-garde-fous.py [--keep] [--only G2,G7]   (staging seul)
"""
import argparse, json, os, sys, time, urllib.parse, urllib.request, urllib.error, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

ENV = "staging"
SINK_HOST, SINK_PORT, DEAD_PORT = "127.0.0.1", 1025, 1026
STAMP = str(int(time.time()))[-7:]
WID = f"gfcheck{STAMP}"
RUN_DOM, SENDER_DOM = "example.com", "agences-veridian.fr"
SENDERS = [f"gf-bot-{x}-{STAMP}@{SENDER_DOM}" for x in "ab"]
RESULT = {}
FAST = {"google": 60, "corporate": 60}  # sans quoi le débit par défaut (0,3 par minute) décide de tout


class NonAbouti(Exception):
    pass


def log(m):
    print(f"[gf] {m}", file=sys.stderr, flush=True)


def record(gate, state, detail):
    RESULT.pop(gate.split("_")[0] + "_", None)
    RESULT[gate] = (state, detail)
    log(f"{state:11} {gate} : {detail}")


def api(method, path, body=None, params=None, ok=(200, 201)):
    for attempt in range(4):
        st, d = nc.call_jwt(ENV, method, path, TOKEN, body=body, params=params)
        if st in ok:
            return d
        if st in (302, 502, 503, 504) and attempt < 3:  # staging qui se réveille ou redémarre
            time.sleep(8)
            continue
        raise NonAbouti(f"{method} {path} : HTTP {st} {str(d)[:160]}")


def facts():
    """Envois du journal : adresse -> (expéditeur retenu, contenu {hash, open_pixel})."""
    out = {}
    for d in api("GET", "/api/veridian/decisions.list", params={"workspace_id": WID, "outcome": "sent", "limit": 100, "trace": "true"}).get("decisions") or []:
        t = d.get("trace") or {}
        frm = next((c.get("from") for c in t.get("candidates") or [] if c.get("outcome") == "selected"), None)
        out[d["contact_email"]] = (frm, t.get("content"))
    return out


def messages(broadcast):
    d = api("GET", "/api/messages.list", params={"workspace_id": WID, "broadcast_id": broadcast, "limit": 100})
    return d.get("messages") or []


def wait(what, cond, seconds, every=3):
    end = time.time() + seconds
    while time.time() < end:
        v = cond()
        if v:
            return v
        time.sleep(every)
    return None


def integration(name, port, senders):
    d = api("POST", "/api/workspaces.createIntegration", {"workspace_id": WID, "name": name, "type": "email", "provider": {
        "kind": "smtp", "smtp": {"host": SINK_HOST, "port": port, "use_tls": False},
        "senders": [{"id": f"s{i}{port}", "email": e, "name": f"Bot {i}", "is_default": i == 0} for i, e in enumerate(senders)],
        "rate_limit_per_minute": 600, "veridian_sending_window": {"start_hour": 0, "end_hour": 24, "timezone": "UTC"}}})
    return d["integration_id"]


def use_profile(pid):
    ws = api("GET", "/api/workspaces.get", params={"id": WID})["workspace"]
    hosts = {i["id"]: ((i.get("email_provider") or {}).get("smtp") or {}).get("host") for i in ws.get("integrations") or []}
    if set(hosts.values()) != {SINK_HOST}:
        raise NonAbouti(f"un profil ne vise pas le puits ({sorted(set(map(str, hosts.values())))}) : rien n'est envoyé")
    s = ws["settings"]
    s["marketing_email_provider_id"] = s["transactional_email_provider_id"] = pid
    api("POST", "/api/workspaces.update", {"id": WID, "name": ws["name"], "settings": s})


def template(tid, sender):
    tree = {"id": "root", "type": "mjml", "attributes": {"version": "4.0.0"}, "children": [{"id": "b", "type": "mj-body", "children": [
        {"id": "s", "type": "mj-section", "children": [{"id": "c", "type": "mj-column", "children": [
            {"id": "t", "type": "mj-text", "content": "{Bonjour|Salut|Coucou} {cher prospect|à vous}, {voici|découvrez} votre audit. {Cordialement|Bien à vous|Merci}."}]}]}]}]}
    api("POST", "/api/templates.create", {"workspace_id": WID, "id": tid, "name": tid, "channel": "email", "category": "marketing",
        "email": {"sender_id": sender, "subject": "{Votre|Un} audit {gratuit|offert}", "visual_editor_tree": tree}})


def campaign(name, contacts, meta, tid="gftpl"):
    lid = name.replace("-", "")
    api("POST", "/api/lists.create", {"workspace_id": WID, "id": lid, "name": lid, "is_double_optin": False, "is_public": False})
    r = api("POST", "/api/contacts.import", {"workspace_id": WID, "subscribe_to_lists": [lid],
            "contacts": [{"email": e, "custom_string_5": c} for e, c in contacts]})
    if (r.get("report") or {}).get("state") != "complet":
        raise NonAbouti(f"import {name} : {(r.get('report') or {}).get('state')}")
    d = api("POST", "/api/broadcasts.create", {"workspace_id": WID, "name": name, "audience": {"list": lid, "exclude_unsubscribed": True},
        "test_settings": {"enabled": False, "sample_percentage": 100, "variations": [{"variation_name": "a", "template_id": tid}]}, "metadata": meta})
    bid = (d.get("broadcast") or d)["id"]
    api("POST", "/api/broadcasts.schedule", {"workspace_id": WID, "id": bid, "send_now": True})
    return bid


def sim(**body):
    st, d = nc.call_hmac(ENV, "POST", "/api/veridian/admin/cold-simulate", {"workspace_id": WID, **body})
    if st != 200:
        raise NonAbouti(f"cold-simulate {body.get('mode')} : HTTP {st} {str(d)[:120]}")
    return d


def sent_ok(m):
    return bool(m.get("sent_at")) and not m.get("failed_at")


def campaign_a():
    """G2 classe exclue, G7 pré-filtre : ce qui doit partir part (témoin), le reste échoue avec sa raison."""
    a = {"google": f"gf-g-{STAMP}@gmail.com", "microsoft": f"gf-m-{STAMP}@outlook.com", "corp": f"gf-c-{STAMP}@{RUN_DOM}",
         "jetable": f"gf-d-{STAMP}@mailinator.com", "mort": f"gf-x-{STAMP}@nonexistent-{STAMP}.invalid"}
    bid = campaign(f"gfa-{STAMP}", [(a["google"], "google"), (a["microsoft"], "microsoft"), (a["corp"], "corporate"),
                                    (a["jetable"], "corporate"), (a["mort"], "corporate")],
                   {"veridian_excluded_provider_classes": ["microsoft"], "veridian_provider_class_rates": FAST})
    wait("A", lambda: len([m for m in messages(bid) if m.get("sent_at") or m.get("failed_at")]) >= 5, 150)
    ms = {m["contact_email"]: m for m in messages(bid)}
    if not (ms.get(a["google"]) and sent_ok(ms[a["google"]]) and ms.get(a["corp"]) and sent_ok(ms[a["corp"]])):
        raise NonAbouti("campagne A : le témoin (google, corporate) n'est pas parti, une absence ne prouverait rien")
    def refused(k):
        m = ms.get(a[k])
        return bool(m and m.get("failed_at")), (m or {}).get("status_info")
    r, why = refused("microsoft")
    record("G2_exclusion", "PASS" if r else "FAIL", f"microsoft refusé={r} ({why}), google parti")
    (r1, w1), (r2, w2) = refused("jetable"), refused("mort")
    record("G7_prefiltre", "PASS" if r1 and r2 else "FAIL", f"jetable refusé={r1} ({w1}), domaine mort refusé={r2} ({w2}), corporate valide parti")


def campaign_b():
    """G3 étalement par classe, G8 contenus différents, G10 expéditeurs en rotation : lus sur les trois envois google."""
    g = [f"gf-bg{i}-{STAMP}@gmail.com" for i in (1, 2, 3)]
    corp = f"gf-bc-{STAMP}@{RUN_DOM}"
    bid = campaign(f"gfb-{STAMP}", [(x, "google") for x in g] + [(corp, "corporate")], {"veridian_provider_class_rates": {"google": 1, "corporate": 60}})
    wait("B", lambda: len([m for m in messages(bid) if sent_ok(m)]) >= 4, 330, 6)
    ms = {m["contact_email"]: m for m in messages(bid)}
    times = sorted(nc_time(ms[x]["sent_at"]) for x in g if x in ms and sent_ok(ms[x]))
    if len(times) < 3 or corp not in ms or not sent_ok(ms[corp]):
        raise NonAbouti(f"campagne B : {len(times)} google sur 3 partis, corporate {'parti' if corp in ms and sent_ok(ms[corp]) else 'absent'}")
    gaps = [round(b - a) for a, b in zip(times, times[1:])]
    early = nc_time(ms[corp]["sent_at"]) < times[1]
    record("G3_etalement", "PASS" if min(gaps) >= 45 and early else "FAIL", f"google à 1 par minute : écarts {gaps} s ; corporate parti avant le deuxième google={early}")
    f = facts()
    if any(x not in f for x in g + [corp]):
        raise NonAbouti("campagne B : un envoi n'est pas au journal des décisions")
    contents = [f[x][1] for x in g + [corp]]
    if any(c is None for c in contents):
        for k in ("G8_contenu", "G9_pixel"):
            record(k, "NON ABOUTI", "le journal des décisions ne porte pas encore le contenu (version servie trop ancienne)")
    else:
        n = len({c["hash"] for c in contents[:3]})
        record("G8_contenu", "PASS" if n >= 2 else "FAIL", f"{n} contenus différents sur 3 envois de la même classe")
        off, on = [c["open_pixel"] for c in contents[:3]], contents[3]["open_pixel"]
        record("G9_pixel", "PASS" if not any(off) and on else "FAIL", f"pixel vers google : {off} (attendu aucun), vers corporate : {on} (attendu oui)")
    froms = {f[x][0] for x in g + [corp]}
    record("G10_rotation", "PASS" if set(SENDERS) <= froms else "FAIL", f"expéditeurs retenus : {sorted(str(x).split('@')[0] for x in froms)}")


def nc_time(s):
    import datetime
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()


def simulate():
    """G4 plafonds du jour (destinataire, classe), G5 par expéditeur, G6 fenêtre : prédicat exact, ouvert puis fermé puis rouvert."""
    dc = f"gf-cap-{STAMP}@{RUN_DOM}"
    cap = lambda n: sim(mode="daily_cap_decision", contact_email=dc, per_recipient_cap=n)["would_be_capped"]
    before = cap(1)
    seeded = sim(mode="seed_sent", contact_email=dc, count=1)["sent_today"]
    seen = (before, seeded, cap(1), cap(2))
    yc = lambda n: sim(mode="class_cap_decision", provider_class="yahoo_aol", class_cap=n)
    base = yc(100000)["sent_today"]
    c0 = yc(base + 2)["would_be_capped"]
    sim(mode="seed_sent", contact_email=f"gf-capclass-{STAMP}@yahoo.com", count=2, provider_class="yahoo_aol")
    c1 = yc(base + 2)["would_be_capped"]
    record("G4_plafond_jour", "PASS" if seen == (False, 1, True, False) and (c0, c1) == (False, True) else "FAIL", f"destinataire {seen} attendu (False, 1, True, False) ; classe {(c0, c1)} attendu (False, True)")
    snd = f"gf-warm-{STAMP}@{SENDER_DOM}"
    ps = lambda n: sim(mode="per_sender_cap_decision", sender_email=snd, per_sender_cap=n)["would_be_capped"]
    s0 = ps(2)
    sim(mode="seed_sent", contact_email=f"gf-any-{STAMP}@{RUN_DOM}", count=2, sender_email=snd)
    seen = (s0, ps(2), ps(3))
    record("G5_par_expediteur", "PASS" if seen == (False, True, False) else "FAIL", f"{seen} attendu (False, True, False)")
    day = (int(time.strftime("%w", time.gmtime())) + 3) % 7
    shut = sim(mode="sending_window_decision", sending_window={"days": [day], "start_hour": 9, "end_hour": 18, "timezone": "UTC"})
    opened = sim(mode="sending_window_decision", sending_window={"start_hour": 0, "end_hour": 24, "timezone": "UTC"})
    good = shut.get("would_be_skipped") is True and (shut.get("next_opening_unix") or 0) > time.time() and opened.get("within") is True
    record("G6_fenetre", "PASS" if good else "FAIL", f"jour fermé : reporté={shut.get('would_be_skipped')} ; 24 h : ouvert={opened.get('within')}")


def circuit():
    """G1 disjoncteur : un profil en panne n'est pas martelé. Port fermé, rien ne peut partir."""
    pid = integration("panne", DEAD_PORT, [f"gf-cb-{STAMP}@{SENDER_DOM}"])
    use_profile(pid)
    template("gftplcb", f"s0{DEAD_PORT}")
    n = 8
    bid = campaign(f"gfcb-{STAMP}", [(f"gf-cb-{STAMP}-{i}@gmail.com", "google") for i in range(n)], {"veridian_provider_class_rates": FAST}, "gftplcb")
    def state():
        d = api("GET", "/api/veridian/queue.explain", params={"workspace_id": WID})
        return {str(g.get("reason") or "").split("/")[0]: g.get("count") or 0 for g in d.get("groups") or []}
    q = wait("CB", lambda: (lambda s: s if any("circuit" in k for k in s) else None)(state()), 120, 5) or state()
    ms = messages(bid)
    out = [m for m in ms if sent_ok(m)]
    tried = [m for m in ms if m.get("failed_at") and "refused" in str(m.get("status_info")).lower()]
    opened = sum(v for k, v in q.items() if "circuit" in k)
    if not tried and not out:
        raise NonAbouti(f"G1 : aucune tentative refusée n'est visible, la panne n'a pas été exercée (file {q})")
    record("G1_disjoncteur", "PASS" if not out and opened > 0 and len(tried) < n else "FAIL",
           f"{len(tried)} tentative(s) refusée(s) sur {n}, {opened} retenue(s) par le disjoncteur, {len(out)} partie(s) (attendu 0)")


def wipe():
    for _ in range(12):  # un redémarrage du staging pendant la mesure ne doit pas laisser d'espace derrière lui
        st, _d = nc.call_hmac(ENV, "POST", "/api/veridian/admin/wipe-test-tenants", {"tenant_ids": [WID], "safety_client_prefixes": ["canary"]})
        if st == 200:
            return True
        time.sleep(15)
    return False


def main():
    global TOKEN
    ap = argparse.ArgumentParser()
    ap.add_argument("--keep", action="store_true")
    ap.add_argument("--only", default="")
    a = ap.parse_args()
    if "staging" not in nc.BASES[ENV]:
        raise NonAbouti("staging seulement")
    owner = f"gf-{STAMP}@e2e.veridian.site"
    sha = nc.call_hmac(ENV, "GET", "/api/version")[1].get("git_sha")
    st, p = nc.call_hmac(ENV, "POST", "/api/tenants/provision", {"tenant_id": WID, "owner_email": owner, "plan": "free",
                         "owner_hub_user_id": str(uuid.uuid5(uuid.NAMESPACE_URL, owner))})
    if st != 200:
        raise NonAbouti(f"provision : HTTP {st}")
    log(f"espace {WID}, staging {str(sha)[:8]}")
    try:
        TOKEN = nc.owner_jwt(ENV, WID)
        use_profile(integration("puits", SINK_PORT, SENDERS))
        template("gftpl", f"s0{SINK_PORT}")
        only = set(filter(None, a.only.split(",")))
        for gates, run in ((("G2", "G7"), campaign_a), (("G3", "G8", "G9", "G10"), campaign_b), (("G4", "G5", "G6"), simulate), (("G1",), circuit)):
            if only and not only & set(gates):
                continue
            try:
                run()
            except NonAbouti as e:
                for g in gates:
                    if not any(k.startswith(g + "_") for k in RESULT):
                        record(g + "_", "NON ABOUTI", str(e))
        if nc.call_hmac(ENV, "GET", "/api/version")[1].get("git_sha") != sha:
            raise NonAbouti("le staging a été redéployé pendant la mesure : rejouer")
    finally:
        if not a.keep and not wipe():
            log(f"🔴 espace {WID} non effacé : notifuse-admin --env staging wipe {WID}")
    for g, (s, d) in sorted(RESULT.items(), key=lambda x: int("".join(c for c in x[0].split("_")[0] if c.isdigit()))):
        print(f"{s:11} {g:18} {d}")
    states = [s for s, _ in RESULT.values()]
    return 1 if "FAIL" in states else 2 if "NON ABOUTI" in states else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except NonAbouti as e:
        print(f"NON ABOUTI : {e}", file=sys.stderr)
        sys.exit(2)
