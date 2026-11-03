"""mail:* — la boîte unifiée de Notifuse (L1, 29/10/2026), par API : lire chaque mail envoyé et reçu en entier, dans son
fil, et envoyer ou répondre comme avec un client mail (sans IMAP ni SMTP). Routes : /api/veridian/mail.*.

Module séparé de notifuse_common (qu'il importe, jamais l'inverse au chargement) ; enregistré par register(sub).
Fonctions de rendu pures, testées (tests/test_mail.py).
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

import notifuse_common as nc

KINDS = ("sent", "reply_human", "reply_auto", "bounce_hard", "bounce_soft", "dsn", "other")
KIND_FR = {
    "sent": "envoyé", "reply_human": "réponse", "reply_auto": "réponse automatique", "bounce_hard": "rebond définitif",
    "bounce_soft": "rebond temporaire", "dsn": "rapport de livraison", "other": "autre",
}
DIR_FR = {"in": "reçu", "out": "envoyé"}


def _kind(k):
    return KIND_FR.get(k, k or "-")


def _addr(name, addr):
    return f"{name} <{addr}>" if name and addr else (addr or name or "-")


def _size(n):
    try:
        n = int(n)
    except (TypeError, ValueError):
        return "-"
    for unit in ("o", "Ko", "Mo", "Go"):
        if n < 1024 or unit == "Go":
            return f"{n} {unit}" if unit == "o" else f"{n:.1f} {unit}"
        n /= 1024.0


# --------------------------------------------------------------------------- rendu (fonctions pures)

def format_boxes(data):
    rows = []
    for b in data.get("mailboxes") or []:
        sync = b.get("sync") or {}
        etat = "-"
        if sync:
            etat = ("ERREUR : " + sync["last_error"][:60]) if sync.get("last_error") else f"lu jusqu'à l'UID {sync.get('last_uid')}"
            if sync.get("last_ok_at"):
                etat += f" ({nc._ts(sync['last_ok_at'])})"
        rows.append((b.get("profile_name") or b.get("id"), b.get("address") or "-", b.get("count_in", 0), b.get("count_out", 0),
                     b.get("count_unhandled", 0), nc._ts(b.get("last_message_at")), etat, b.get("id")))
    if not rows:
        return ["(aucune boîte)"]
    return nc._table(rows, ("profil", "adresse", "reçus", "envoyés", "à traiter", "dernier", "lecture IMAP", "id"))


def format_list(data):
    rows = []
    for m in data.get("messages") or []:
        rows.append((nc._ts(m.get("occurred_at")), "←" if m.get("direction") == "in" else "→", _kind(m.get("kind")),
                     (m.get("contact_email") or "-")[:34], (m.get("subject") or "(sans objet)")[:48],
                     "✓" if m.get("handled_at") else "", "pj%d" % m["attachment_count"] if m.get("attachment_count") else "",
                     m.get("id")))
    L = nc._table(rows, ("date (UTC)", "", "nature", "contact", "objet", "traité", "pj", "id")) if rows else ["(aucun message)"]
    if data.get("next_cursor"):
        L.append(f"… suite : --cursor {data['next_cursor']}")
    return L


def format_message(m, with_headers=True, with_body=True):
    """Fiche complète d'un message : en-têtes lisibles, DSN expliqué, lien, pièces, corps."""
    L = [f"{_kind(m.get('kind')).upper()} · {DIR_FR.get(m.get('direction'), '-')} · {nc._ts(m.get('occurred_at'))} UTC · id {m.get('id')}",
         f"Objet   : {m.get('subject') or '(sans objet)'}",
         f"De      : {_addr(m.get('from_name'), m.get('from'))}",
         f"À       : {', '.join(m.get('to') or []) or '-'}"]
    if m.get("cc"):
        L.append(f"Cc      : {', '.join(m['cc'])}")
    L.append(f"Contact : {m.get('contact_email') or '-'} · fil {m.get('thread_id')}")
    if m.get("profile_id"):
        L.append(f"Profil  : {m['profile_id']}")
    mb = m.get("mailbox_ref")
    if mb:
        L.append(f"Boîte   : {mb.get('integration_id')} · {mb.get('folder')} · UID {mb.get('uid')} (validité {mb.get('uid_validity')})")
    if m.get("message_id"):
        L.append(f"Message-ID : {m['message_id']}" + (f" · en réponse à {m['in_reply_to']}" if m.get("in_reply_to") else ""))
    lk = m.get("link")
    if lk:
        bits = [f"envoi {lk['sent_message_id']}"] if lk.get("sent_message_id") else []
        for k, label in (("automation_id", "automation"), ("node_id", "nœud"), ("broadcast_id", "campagne")):
            if lk.get(k):
                bits.append(f"{label} {lk[k]}")
        bits.append(f"rattaché par {lk.get('match_type') or '-'}")
        L.append("Lien    : " + " · ".join(bits))
    if m.get("body_source") and m.get("direction") == "out":
        src = {"captured": "rendu exact tel que parti", "rerendered": "reconstitué depuis le modèle d'époque",
               "unavailable": "corps non conservé"}.get(m["body_source"], m["body_source"])
        L.append(f"Corps   : {src}")
    if m.get("truncated"):
        L.append("! message lu tronqué (trop volumineux)")
    d = m.get("dsn")
    if d:
        L += ["", "RAPPORT DE NON-REMISE",
              f"  {d.get('reason_fr')}",
              f"  code {d.get('code') or '-'} ({d.get('severity') or '-'}) · action {d.get('action') or '-'} · fournisseur {d.get('provider') or '-'} · adresse {d.get('recipient') or '-'}"]
        if d.get("advice_fr"):
            L.append(f"  À faire : {d['advice_fr']}")
        if d.get("diagnostic"):
            L.append(f"  Diagnostic du serveur : {d['diagnostic']}")
    atts = m.get("attachments") or []
    if atts:
        L += ["", "PIÈCES JOINTES"]
        for a in atts:
            L.append(f"  [{a.get('index')}] {a.get('name')} · {a.get('content_type')} · {_size(a.get('size'))}"
                     + (" · incorporée" if a.get("inline") else "") + ("" if a.get("available") else " · contenu indisponible"))
    if m.get("handled_at"):
        L.append(f"Traité le {nc._ts(m['handled_at'])} par {m.get('handled_by') or '-'}")
    if m.get("labels"):
        L.append("Libellés : " + ", ".join(m["labels"]))
    if with_headers and m.get("headers"):
        L += ["", "EN-TÊTES"]
        for k in sorted(m["headers"]):
            for v in m["headers"][k]:
                L.append(f"  {k}: {v}")
    if with_body:
        L += ["", "─" * 60, (m.get("body_text") or "(corps vide)").rstrip(), "─" * 60]
    return L


def format_thread(t, with_body=True):
    """Fil en chronologie : jalon, date, sens, objet, puis le corps indenté."""
    L = [f"FIL {t.get('thread_id')} · {t.get('subject') or '(sans objet)'} · {t.get('contact_email') or '-'} · "
         f"{t.get('message_count')} message(s) du {nc._ts(t.get('first_at'))} au {nc._ts(t.get('last_at'))}"]
    labels = {ms.get("id"): ms.get("label") for ms in t.get("milestones") or []}
    for m in t.get("messages") or []:
        arrow = "←" if m.get("direction") == "in" else "→"
        L += ["", f"{arrow} {labels.get(m.get('id')) or _kind(m.get('kind'))} · {nc._ts(m.get('occurred_at'))} · {_kind(m.get('kind'))} · id {m.get('id')}",
              f"  Objet : {m.get('subject') or '(sans objet)'}"]
        d = m.get("dsn")
        if d:
            L.append(f"  {d.get('reason_fr')}")
        if with_body and (m.get("body_text") or "").strip():
            body = m["body_text"].strip().splitlines()
            L += ["    " + ln for ln in body[:40]]
            if len(body) > 40:
                L.append(f"    … ({len(body) - 40} ligne(s) de plus : mail:get --id {m.get('id')})")
        if m.get("attachments"):
            L.append("  pièces : " + ", ".join(a.get("name", "?") for a in m["attachments"] if not a.get("inline")))
    return L


def format_threads(data):
    rows = [(nc._ts(t.get("last_at")), t.get("message_count"), _kind(t.get("last_kind")), (t.get("subject") or "")[:50], t.get("thread_id"))
            for t in data.get("threads") or []]
    return nc._table(rows, ("dernier", "messages", "dernier mail", "objet", "fil")) if rows else ["(aucun fil)"]


# --------------------------------------------------------------------------- commandes

def _get(a, route, params):
    jwt = nc.apikey_for(a.env, a.workspace)
    params = dict(params)
    params["workspace_id"] = a.workspace
    return nc.call_jwt(a.env, "GET", route, jwt, params=params)


def _post(a, route, body):
    jwt = nc.apikey_for(a.env, a.workspace)
    body = dict(body)
    body["workspace_id"] = a.workspace
    return nc.call_jwt(a.env, "POST", route, jwt, body=body)


def _fail(st, data):
    if st in (200, 201, 202):
        return False
    msg = data.get("error") if isinstance(data, dict) else None
    print(f"✗ HTTP {st} : {msg or data}", file=sys.stderr)
    sys.exit(2)


def _show(a, data, fmt):
    print(json.dumps(data, indent=2, ensure_ascii=False) if getattr(a, "json", False) else "\n".join(fmt(data)))


def cmd_boxes(a):
    st, data = _get(a, "/api/veridian/mail.mailboxes.list", {})
    _fail(st, data)
    _show(a, data, format_boxes)


def cmd_list(a):
    kinds = [k for k in (a.kind or "").split(",") if k]
    bad = [k for k in kinds if k not in KINDS]
    if bad:
        nc.die(f"--kind : nature inconnue {', '.join(bad)} (permis : {', '.join(KINDS)}).")
    handled = "true" if a.handled else ("false" if a.unhandled else None)
    params = {"mailbox": a.mailbox, "contact_email": a.contact, "thread_id": a.thread, "kind": ",".join(kinds) or None,
              "direction": a.direction, "handled": handled, "since": a.since, "until": a.until, "q": a.q,
              "body": "true" if a.body else None, "include_duplicates": "true" if getattr(a, "include_duplicates", False) else None,
              "limit": a.limit, "cursor": a.cursor}
    st, data = _get(a, "/api/veridian/mail.messages.list", {k: v for k, v in params.items() if v is not None})
    _fail(st, data)
    _show(a, data, format_list)


def cmd_get(a):
    st, data = _get(a, "/api/veridian/mail.messages.get", {"id": a.id})
    _fail(st, data)
    if a.json:
        print(json.dumps(data, indent=2, ensure_ascii=False))
    else:
        print("\n".join(format_message(data["message"], with_headers=a.headers)))


def _download(a, route, params):
    """GET binaire (message brut, pièce jointe) : rend (statut, octets ou erreur JSON, en-têtes)."""
    jwt = nc.apikey_for(a.env, a.workspace)
    params = dict(params)
    params["workspace_id"] = a.workspace
    url = nc.BASES[a.env] + route + "?" + urllib.parse.urlencode(params)
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {jwt}"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, r.read(), dict(r.headers)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw), {}
        except Exception:
            return e.code, {"error": raw[:300].decode("utf-8", "replace")}, {}
    except urllib.error.URLError as e:
        nc.die(f"Notifuse injoignable : {e.reason}")


def _save_or_print(data, out_path):
    if out_path and out_path != "-":
        with open(out_path, "wb") as f:
            f.write(data)
        print(f"✓ {len(data)} octets écrits dans {out_path}", file=sys.stderr)
    else:
        sys.stdout.buffer.write(data)


def cmd_raw(a):
    st, body, hdr = _download(a, "/api/veridian/mail.messages.raw", {"id": a.id})
    if st != 200:
        _fail(st, body)
    if hdr.get("X-Veridian-Reconstructed") == "true":
        print("! mail envoyé : message reconstitué depuis le rendu conservé (en-tête X-Veridian-Reconstructed)", file=sys.stderr)
    _save_or_print(body, a.out)


def cmd_attachment(a):
    st, body, hdr = _download(a, "/api/veridian/mail.messages.attachment", {"id": a.id, "index": a.index})
    if st != 200:
        _fail(st, body)
    out_path = a.out
    if not out_path:
        cd = hdr.get("Content-Disposition", "")
        name = "piece-%s" % a.index
        if "filename=" in cd:
            name = cd.split("filename=", 1)[1].strip('"; ')
        out_path = os.path.basename(name) or name
    _save_or_print(body, out_path)


def cmd_thread(a):
    if not (a.thread or a.contact):
        nc.die("mail:thread exige --thread T ou --contact EMAIL.")
    params = {"thread_id": a.thread} if a.thread else {"contact_email": a.contact}
    st, data = _get(a, "/api/veridian/mail.threads.get", params)
    _fail(st, data)
    if a.json:
        print(json.dumps(data, indent=2, ensure_ascii=False))
        return
    threads = [data["thread"]] if data.get("thread") else data.get("threads") or []
    if not threads:
        print("(aucun fil)")
    print("\n\n".join("\n".join(format_thread(t, with_body=not a.no_body)) for t in threads))


def cmd_threads(a):
    st, data = _get(a, "/api/veridian/mail.threads.list", {"contact_email": a.contact})
    _fail(st, data)
    _show(a, data, format_threads)


def cmd_update(a):
    body = {"id": a.id}
    if a.handled:
        body["handled"] = True
    if a.unhandled:
        body["handled"] = False
    if a.clear_labels:
        body["labels"] = []
    if a.label:
        body["labels"] = list(a.label)
    if "handled" not in body and "labels" not in body:
        nc.die("mail:update exige --handled, --unhandled, --label L ou --clear-labels.")
    st, data = _post(a, "/api/veridian/mail.messages.update", body)
    _fail(st, data)
    m = data["message"]
    if a.json:
        print(json.dumps(data, indent=2, ensure_ascii=False))
    else:
        print(f"✓ {m['id']} · {'traité par ' + m.get('handled_by', '?') if m.get('handled_at') else 'à traiter'}"
              + (f" · libellés {', '.join(m['labels'])}" if m.get("labels") else ""))


def _content(a):
    text, html = a.text, None
    if a.text_file:
        with open(a.text_file, encoding="utf-8") as f:
            text = f.read()
    if a.html_file:
        with open(a.html_file, encoding="utf-8") as f:
            html = f.read()
    return text, html


def _send(a, route, body, what):
    text, html = _content(a)
    if text:
        body["text"] = text
    if html:
        body["html"] = html
    if not (text or html):
        nc.die("exige --text, --text-file ou --html-file.")
    if a.cc:
        body["cc"] = list(a.cc)
    if not a.yes:
        view = {k: (v if k not in ("text", "html") else f"({len(v)} caractères)") for k, v in body.items()}
        print(f"SIMULATION (rien n'est envoyé) : POST {route}\n{json.dumps(view, indent=2, ensure_ascii=False)}\nAjoute --yes pour {what}.")
        return
    st, data = _post(a, route, body)
    if st == 409 and isinstance(data, dict) and data.get("refusal"):
        r = data["refusal"]
        print(f"✗ REFUSÉ ({r.get('gate') or '-'}) : {r.get('reason')}", file=sys.stderr)
        sys.exit(1)
    _fail(st, data)
    if a.json:
        print(json.dumps(data, indent=2, ensure_ascii=False))
    else:
        print(f"✓ en file : {data.get('message_id')} · fil {data.get('thread_id')}\n  {data.get('note')}")


def cmd_send(a):
    body = {"profile_id": a.profile, "to": a.to, "subject": a.subject}
    _send(a, "/api/veridian/mail.messages.send", body, "mettre ce mail en file (mêmes plafonds que les séquences)")


def cmd_reply(a):
    body = {"in_reply_to_message": a.id}
    if a.profile:
        body["profile_id"] = a.profile
    if a.to:
        body["to"] = a.to
    if a.subject:
        body["subject"] = a.subject
    _send(a, "/api/veridian/mail.messages.reply", body, "mettre cette réponse en file (même profil, dans le fil)")


def parse_sse(lines):
    """Découpe un flux SSE (itérable de lignes) en événements {event, id, data}. Fonction pure, testée."""
    cur = {}
    for raw in lines:
        line = raw.rstrip("\r\n")
        if line == "":
            if cur.get("event"):
                yield cur
            cur = {}
        elif line.startswith(":"):
            continue
        elif ": " in line:
            k, v = line.split(": ", 1)
            cur[k] = v
    if cur.get("event"):
        yield cur


def cmd_events(a):
    jwt = nc.apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "direction": a.direction}
    if a.since_seq is not None:
        params["since_seq"] = a.since_seq
    if a.kind:
        params["kind"] = a.kind
    url = nc.BASES[a.env] + "/api/veridian/mail.events?" + urllib.parse.urlencode(params)
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {jwt}", "Accept": "text/event-stream"})
    deadline = time.time() + a.timeout
    n = 0
    try:
        with urllib.request.urlopen(req, timeout=max(5, a.timeout)) as r:
            for ev in parse_sse(line.decode("utf-8", "replace") for line in r):
                if ev["event"] == "reconnect":
                    break
                n += 1
                data = json.loads(ev.get("data") or "{}")
                if a.json:
                    print(json.dumps({"seq": ev.get("id"), "event": ev["event"], **data}, ensure_ascii=False), flush=True)
                else:
                    print(f"#{ev.get('id')} {ev['event']} · {_kind(data.get('kind'))} · {data.get('contact_email')} · {data.get('subject')} · id {data.get('id')}", flush=True)
                if n >= a.max or time.time() > deadline:
                    break
    except urllib.error.HTTPError as e:
        try:
            _fail(e.code, json.loads(e.read()))
        except ValueError:
            _fail(e.code, {"error": f"HTTP {e.code}"})
    except (TimeoutError, OSError):
        pass  # délai écoulé : normal pour un flux sans nouveau message
    if n == 0 and not a.json:
        print("(aucun nouveau message pendant l'attente)", file=sys.stderr)


# --------------------------------------------------------------------------- enregistrement

COMMANDS = ("mail:boxes", "mail:list", "mail:get", "mail:raw", "mail:attachment", "mail:thread", "mail:threads",
            "mail:update", "mail:send", "mail:reply", "mail:events")


def register(sub):
    def add(name, help, func, routes):
        sp = sub.add_parser(name, help=help)
        sp.add_argument("workspace", nargs="?", default=None)
        sp.set_defaults(func=func, _routes=routes)
        return sp

    def jsonflag(sp):
        sp.add_argument("--json", action="store_true", help="sortie JSON brute")

    sp = add("mail:boxes", "boîtes (une par profil d'envoi et par adresse) : compteurs et état de la lecture IMAP", cmd_boxes, ["/api/veridian/mail.mailboxes.list"])
    jsonflag(sp)

    sp = add("mail:list", "messages envoyés et reçus (filtres : boîte, contact, fil, nature, traité, dates, recherche)", cmd_list, ["/api/veridian/mail.messages.list"])
    sp.add_argument("--mailbox", help="id de boîte (mail:boxes)")
    sp.add_argument("--contact", help="adresse du contact")
    sp.add_argument("--thread", help="id de fil")
    sp.add_argument("--kind", help="natures séparées par des virgules : " + ",".join(KINDS))
    sp.add_argument("--direction", choices=["in", "out"])
    g = sp.add_mutually_exclusive_group()
    g.add_argument("--handled", action="store_true", help="seulement les messages traités")
    g.add_argument("--unhandled", action="store_true", help="seulement les messages à traiter")
    sp.add_argument("--since", help="AAAA-MM-JJ ou RFC 3339")
    sp.add_argument("--until", help="AAAA-MM-JJ ou RFC 3339")
    sp.add_argument("-q", help="recherche dans l'objet, les adresses (et les corps avec --body)")
    sp.add_argument("--body", action="store_true", help="cherche aussi dans les corps (les 1500 derniers messages du filtre)")
    sp.add_argument("--include-duplicates", action="store_true", dest="include_duplicates",
                    help="rend aussi les copies d'un message déjà présent (copie Sieve, alias), liées à l'original par duplicate_of")
    sp.add_argument("--limit", type=int, help="1 à 200 (défaut 50)")
    sp.add_argument("--cursor", help="suite d'une liste (affichée en bas)")
    jsonflag(sp)

    sp = add("mail:get", "un message en entier : en-têtes, DSN expliqué, lien, pièces jointes, corps", cmd_get, ["/api/veridian/mail.messages.get"])
    sp.add_argument("--id", required=True)
    sp.add_argument("--headers", action="store_true", help="affiche aussi tous les en-têtes")
    jsonflag(sp)

    sp = add("mail:raw", "message brut RFC 822 (stdout ou --out FICHIER) ; un mail envoyé est reconstitué", cmd_raw, ["/api/veridian/mail.messages.raw"])
    sp.add_argument("--id", required=True)
    sp.add_argument("--out", help="fichier .eml (défaut : stdout)")

    sp = add("mail:attachment", "contenu d'une pièce jointe (fichier --out, défaut : son nom)", cmd_attachment, ["/api/veridian/mail.messages.attachment"])
    sp.add_argument("--id", required=True)
    sp.add_argument("--index", type=int, required=True, help="numéro de la pièce (mail:get)")
    sp.add_argument("--out")

    sp = add("mail:thread", "le fil complet d'un contact ou d'un fil, en chronologie (J0, relances, réponse, rebond)", cmd_thread, ["/api/veridian/mail.threads.get"])
    sp.add_argument("--thread")
    sp.add_argument("--contact")
    sp.add_argument("--no-body", action="store_true", help="sans le texte des mails")
    jsonflag(sp)

    sp = add("mail:threads", "les fils d'un contact (résumés)", cmd_threads, ["/api/veridian/mail.threads.list"])
    sp.add_argument("--contact", required=True)
    jsonflag(sp)

    sp = add("mail:update", "marque traité / à traiter, pose des libellés", cmd_update, ["/api/veridian/mail.messages.update"])
    sp.add_argument("--id", required=True)
    g = sp.add_mutually_exclusive_group()
    g.add_argument("--handled", action="store_true")
    g.add_argument("--unhandled", action="store_true")
    sp.add_argument("--label", action="append", help="remplace les libellés (répétable)")
    sp.add_argument("--clear-labels", action="store_true")
    jsonflag(sp)

    sp = add("mail:send", "envoie un mail par un profil, mêmes plafonds et portes que les séquences (sans --yes : simulation)", cmd_send, ["/api/veridian/mail.messages.send"])
    sp.add_argument("--profile", required=True, help="id du profil d'envoi (mail:boxes)")
    sp.add_argument("--to", required=True)
    sp.add_argument("--subject", required=True)
    sp.add_argument("--cc", action="append")
    sp.add_argument("--text")
    sp.add_argument("--text-file")
    sp.add_argument("--html-file")
    sp.add_argument("--yes", action="store_true", help="envoie pour de vrai (met en file)")
    jsonflag(sp)

    sp = add("mail:reply", "répond à un mail reçu, dans son fil (In-Reply-To, References), par le profil qui l'a reçu (sans --yes : simulation)", cmd_reply, ["/api/veridian/mail.messages.reply"])
    sp.add_argument("--id", required=True, help="id du message reçu auquel on répond")
    sp.add_argument("--profile")
    sp.add_argument("--to")
    sp.add_argument("--subject")
    sp.add_argument("--cc", action="append")
    sp.add_argument("--text")
    sp.add_argument("--text-file")
    sp.add_argument("--html-file")
    sp.add_argument("--yes", action="store_true")
    jsonflag(sp)

    sp = add("mail:events", "flux des nouveaux messages (reprise exacte avec --since-seq) ; sort après --max événements ou --timeout secondes", cmd_events, ["/api/veridian/mail.events"])
    sp.add_argument("--since-seq", type=int, dest="since_seq", help="reprend après ce numéro d'ordre (défaut : seulement le neuf)")
    sp.add_argument("--direction", default="in", choices=["in", "out", "any"])
    sp.add_argument("--kind")
    sp.add_argument("--max", type=int, default=1000)
    sp.add_argument("--timeout", type=int, default=300)
    jsonflag(sp)
