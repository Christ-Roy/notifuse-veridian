#!/usr/bin/env python3
"""Ce qui dure après le retrait d'un membre (fiche 91), sur STAGING : accès de boîte (vrais IMAP et SMTP), flux de la boîte unifiée, gel, suspension, compte
supprimé au Hub. Un témoin (propriétaire) garde tout. Sort 0 (prouvé), 1 (défaut), 2 (non abouti). Aucun mail ne part (profil vers un hôte inexistant)."""
import email.utils, imaplib, json, os, re, smtplib, ssl, subprocess, sys, threading, time, urllib.parse, urllib.request, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

ENV, T = "staging", f"{int(time.time()) % 10**8}"
WS, OWNER, M = f"retr{T}", str(uuid.uuid4()), str(uuid.uuid4())
MAILS = {OWNER: f"e2e-retr-{T}-owner@example.com", M: f"e2e-retr-{T}-membre@example.com"}
bad = []


def non_abouti(*x):
    print("NON ABOUTI :", *x); sys.exit(2)


def check(name, ok, seen=""):
    print("ok " if ok else "KO ", name, "" if ok else seen, flush=True)
    if not ok:
        bad.append(name)


def hub(method, path, body=None):
    st, r = nc.call_hmac(ENV, method, path, body)
    return st, (r if isinstance(r, dict) else {"brut": r})


def geste(nom, body):
    return hub("POST", f"/api/tenants/{WS}/{nom}", body)


def lien(who):
    st, r = hub("POST", "/api/sso/issue-magic-link", {"hub_user_id": who, "email": MAILS[who], "email_proven": True})
    return next((v for v in r.values() if isinstance(v, str) and "token=" in v), None) if st == 200 else None


def entrer(url):
    """Suit un lien de connexion ; rend le jeton de session, None si refusé. Jamais à l'écran."""
    try:
        html = urllib.request.urlopen(url, timeout=30).read().decode()
    except urllib.error.HTTPError:
        return None
    m = re.search(r"setItem\('auth_token',\s*\"([^\"]+)\"", html)
    return json.loads(f'"{m.group(1)}"') if m else None


for _ in range(40):
    st, _r = nc.call_anon(ENV, "GET", "/api/veridian/mode")
    if st == 200:
        break
    time.sleep(3)
else:
    non_abouti("staging endormi ou injoignable", st)


def service(nom):
    p = subprocess.run(["nomad", "service", "info", "-json", nom], capture_output=True, text=True, timeout=30)
    svc = json.loads(p.stdout) if p.returncode == 0 and p.stdout.strip().startswith("[") else []
    if not svc:
        non_abouti(nom, "absent du catalogue")
    return svc[0]["Address"], svc[0]["Port"]


HOST, PORT = service("notifuse-staging-imap")
DIRECT = "http://%s:%d" % service("notifuse-staging")  # l'entrée de staging (Sablier) met un flux en tampon : on l'ouvre sans elle
CTX = ssl.create_default_context(); CTX.check_hostname = False; CTX.verify_mode = ssl.CERT_NONE  # adresse du tailnet, certificat du relais


def imap(user, pw):
    """vrai = connexion et dossier ouverts ; faux = refus d'identifiants ; autre chose = non abouti."""
    c = imaplib.IMAP4_SSL(HOST, PORT, ssl_context=CTX, timeout=15)
    try:
        c.login(user, pw)
        return c.select("INBOX")[0] == "OK"
    except imaplib.IMAP4.error as e:
        if "AUTHENTICATIONFAILED" in str(e).upper() or "AUTHENTICATION" in str(e).upper():
            return False
        non_abouti("IMAP", str(e)[:120])
    finally:
        try:
            c.shutdown()
        except OSError:
            pass


def smtp(user, pw):
    """(connexion, réponse au RCPT)."""
    s = smtplib.SMTP(HOST, 587, timeout=20)
    try:
        s.starttls(context=CTX); s.ehlo()
        try:
            s.login(user, pw)
        except smtplib.SMTPAuthenticationError as e:
            return False, e.smtp_code
        s.mail(user)
        return True, s.rcpt("personne@example.com")[0]
    finally:
        s.close()


class Flux(threading.Thread):
    def __init__(self, jeton):
        super().__init__(daemon=True)
        self.fin, self.lignes = None, []
        self.r = urllib.request.urlopen(urllib.request.Request(DIRECT + f"/api/veridian/mail.events?workspace_id={WS}", headers={"Authorization": f"Bearer {jeton}"}), timeout=90)
        self.start()

    def run(self):
        try:
            for l in self.r:
                self.lignes.append(l.decode().strip())
        except Exception:
            pass
        self.fin = time.time()


st, r = hub("POST", "/api/tenants/provision", {"tenant_id": WS, "owner_email": MAILS[OWNER], "owner_hub_user_id": OWNER})
if st != 200:
    non_abouti("espace", st, r)
try:
    st, r = geste("sync-member", {"hub_user_id": M, "email": MAILS[M], "user_email": MAILS[M], "email_proven": True, "role": "member"})
    if st != 200:
        non_abouti("membre", st, r)
    jo, jm = entrer(lien(OWNER)), entrer(lien(M))
    if not jo or not jm:
        non_abouti("sessions", bool(jo), bool(jm))
    st, r = nc.call_jwt(ENV, "POST", "/api/workspaces.createIntegration", jo, body={"workspace_id": WS, "name": "retrait", "type": "email", "provider": {
        "kind": "smtp", "smtp": {"host": "smtp.invalid", "port": 587, "username": "x", "password": "x", "use_tls": True},
        "senders": [{"email": MAILS[OWNER], "name": "TEST", "is_default": True}], "rate_limit_per_minute": 1}})
    prof = r.get("integration_id") if isinstance(r, dict) else None
    if not prof:
        non_abouti("profil", st, str(r)[:160])

    def acces(j):
        st, r = nc.call_jwt(ENV, "POST", "/api/veridian/mail.access.create", j, body={"workspace_id": WS, "profile_id": prof, "label": "e2e"})
        if st != 201:
            non_abouti("accès de boîte", st, r.get("code"))
        return r["username"], r["password"]

    ao, am = acces(jo), acces(jm)
    check("départ : IMAP ouvert aux deux", imap(*ao) and imap(*am))
    check("départ : SMTP ouvert aux deux", smtp(*ao) == (True, 250) and smtp(*am) == (True, 250), f"{smtp(*ao)} {smtp(*am)}")

    # Gel : réversible. La lecture de la console est floutée, la boîte ne peut pas l'être : elle se ferme, puis se rouvre.
    geste("freeze-member", {"hub_user_id": M})
    check("gelé : IMAP refusé", imap(*am) is False)
    check("gelé : SMTP refusé", smtp(*am)[0] is False)
    st, r = nc.call_jwt(ENV, "GET", "/api/veridian/mail.messages.list", jm, params={"workspace_id": WS})
    check("gelé : boîte unifiée refusée", st == 403, st)
    st, r = nc.call_jwt(ENV, "POST", "/api/veridian/mail.access.create", jm, body={"workspace_id": WS, "profile_id": prof, "label": "e2e"})
    check("gelé : aucun accès neuf", st in (402, 403), st)
    geste("unfreeze-member", {"hub_user_id": M})
    check("dégelé : IMAP rouvert, même mot de passe", imap(*am))

    # Suspension : lecture seule (CONTRAT-HUB §5.4), l'envoi se ferme, puis se rouvre.
    hub("POST", "/api/tenants/suspend", {"tenant_id": WS, "reason": "admin_action"})
    check("suspendu : IMAP lit encore", imap(*ao))
    check("suspendu : SMTP n'envoie plus", smtp(*ao)[1] == 550, smtp(*ao))
    hub("POST", "/api/tenants/resume", {"tenant_id": WS})
    check("repris : SMTP renvoie", smtp(*ao) == (True, 250), smtp(*ao))

    # Retrait, flux et session IMAP ouverts.
    fm, fo = Flux(jm), Flux(jo)
    ouverte = imaplib.IMAP4_SSL(HOST, PORT, ssl_context=CTX, timeout=15); ouverte.login(*am); ouverte.select("INBOX")
    time.sleep(3)
    check("flux ouverts avant", fm.fin is None and fo.fin is None)
    emis = lien(M)  # émis avant le retrait, consommé après
    st, r = geste("remove-member", {"hub_user_id": M, "reason": "e2e"})
    t0 = time.time()
    check("remove-member : retiré", st == 200 and r.get("removed") is True, st)
    check("remove-member dit : 1 accès révoqué, 1 reste", r.get("revoked_mail_accesses") == 1 and r.get("active_mail_accesses") == 1, {k: v for k, v in r.items() if "mail" in k})
    check("retiré : IMAP refusé au premier essai", imap(*am) is False)
    check("retiré : SMTP refusé au premier essai", smtp(*am)[0] is False)
    while fm.fin is None and time.time() - t0 < 15:
        time.sleep(0.2)
    check("retiré : son flux fermé", fm.fin is not None and any("access_revoked" in l for l in fm.lignes), fm.lignes[-3:])
    print(f"    flux fermé {((fm.fin or 0) - t0):.1f} s après la réponse")
    coupe = None
    while time.time() - t0 < 20 and coupe is None:
        try:
            if ouverte.noop()[0] != "OK":
                coupe = time.time()
        except (imaplib.IMAP4.error, OSError):
            coupe = time.time()
        time.sleep(0.5)
    check("retiré : session IMAP déjà ouverte coupée", coupe is not None)
    print(f"    session IMAP coupée {((coupe or 0) - t0):.1f} s après la réponse")
    j = entrer(emis)
    st, _r = nc.call_jwt(ENV, "GET", "/api/veridian/mail.messages.list", j or jm, params={"workspace_id": WS})
    check("retiré : un lien déjà émis n'ouvre pas l'espace", st in (401, 403), st)
    st, r = geste("remove-member", {"hub_user_id": M})
    check("rejeu : 0 révoqué, 1 reste", r.get("revoked_mail_accesses") == 0 and r.get("active_mail_accesses") == 1, r)
    check("témoin : IMAP, SMTP et flux intacts", imap(*ao) and smtp(*ao) == (True, 250) and fo.fin is None)
    geste("restore-member", {"hub_user_id": M})
    check("rétabli : l'ancien accès reste révoqué", imap(*am) is False)

    # Compte supprimé au Hub : ni code par e-mail, ni lien déjà émis, ni session neuve.
    avant = lien(M)
    st, _r = nc.call_anon(ENV, "POST", "/api/user.signin", {"email": MAILS[M]})
    check("avant suppression : la connexion répond", st == 200, st)
    am2 = acces(entrer(lien(M)))
    check("avant suppression : accès neuf ouvert", imap(*am2))
    if not avant:
        non_abouti("lien d'avant la suppression")
    hub("POST", "/api/hub/users/sync", {"hub_user_id": M, "deleted": True})
    st, _r = nc.call_anon(ENV, "POST", "/api/user.signin", {"email": MAILS[M]})
    check("supprimé : user.signin refusé", st == 400, st)
    check("supprimé : lien déjà émis refusé", entrer(avant) is None)
    check("supprimé (encore membre) : IMAP refusé", imap(*am2) is False)
    st, r = geste("transfer-owner", {"owner_hub_user_id": OWNER, "reason": "e2e"})
    check("gestes du Hub toujours possibles", st in (200, 409), f"{st} {r}")
finally:
    if "--keep" not in sys.argv:
        hub("POST", "/api/hub/users/sync", {"hub_user_id": M, "deleted": True})
        hub("POST", "/api/veridian/admin/wipe-test-tenants", {"tenant_ids": [WS]})
print("PROUVÉ" if not bad else f"DÉFAUT : {len(bad)}")
sys.exit(1 if bad else 0)
