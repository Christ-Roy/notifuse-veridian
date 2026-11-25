#!/usr/bin/env python3
"""F3 : range UNE réponse humaine dans la boîte d'un espace de STAGING, puis suit la vraie livraison message.received (déclencheur, livreur, signature,
HTTP). Seule la lecture IMAP n'est pas jouée : la ligne est posée dans la table de la boîte, comme l'ingestion le fait. Aucun mail ne part.

    reponse-recue-staging.py <espace> --email contact@exemple.test [--attendre 120]
Sort 0 (livrée en 2xx), 1 (refusée ou en échec), 2 (non abouti : pas d'abonnement, pas de livraison créée, base injoignable)."""
import argparse, json, re, subprocess, sys, time, uuid

a = argparse.ArgumentParser()
a.add_argument("ws"); a.add_argument("--email", required=True); a.add_argument("--attendre", type=int, default=120); a.add_argument("--kind", default="reply_human")
a.add_argument("--match", default="message_id")
a = a.parse_args()
if not re.fullmatch(r"[a-z0-9]{1,40}", a.ws) or not re.fullmatch(r"[\w.+-]+@[\w.-]+", a.email) or not re.fullmatch(r"[a-z_]+", a.kind + a.match):
    sys.exit("argument refusé")


def sql(q):
    p = subprocess.run(["ssh", "dev-pub", "docker exec -i $(docker ps --format '{{.Names}}' | grep '^notifuse-staging-db') "
                        f"sh -c 'psql -qAt -v ON_ERROR_STOP=1 -U $POSTGRES_USER -d notifuse_ws_{a.ws}'"], input=q, capture_output=True, text=True, timeout=60)
    if p.returncode:
        print("NON ABOUTI :", p.stderr.strip()[-200:]); sys.exit(2)
    return [x for x in p.stdout.strip().splitlines() if x]


if sql("SELECT count(*) FROM webhook_subscriptions WHERE enabled AND settings->'event_types' ? 'message.received'") == ["0"]:
    print("NON ABOUTI : aucun abonnement message.received actif sur cet espace"); sys.exit(2)
mid, rfc, thread = str(uuid.uuid4()), f"f3-{uuid.uuid4().hex[:12]}@exemple.test", "t_" + uuid.uuid4().hex[:16]
sql(f"""INSERT INTO veridian_mail_message (id, workspace_id, direction, kind, message_id, thread_id, contact_email, from_addr, to_addrs, subject, received_at, occurred_at, link, dedup_key)
  VALUES ('{mid}', '{a.ws}', 'in', '{a.kind}', '<{rfc}>', '{thread}', lower('{a.email}'), lower('{a.email}'), '{{boite@exemple.test}}', 'Re: essai F3, a ignorer', NOW(), NOW(),
  '{{"match_type":"{a.match}","sent_message_id":"{a.ws}_{mid}"}}', 'mid:{rfc}')""")
print("réponse rangée : message_id", mid, "thread_id", thread)
end, seen = time.time() + a.attendre, None
while time.time() < end:
    r = sql(f"SELECT id, status, attempts, coalesce(last_response_status::text,''), coalesce(left(last_response_body,200),''), coalesce(left(last_error,160),'') FROM webhook_deliveries WHERE event_type='message.received' AND payload->>'message_id'='{mid}'")
    if not r and time.time() > end - a.attendre + 15:
        print("NON ABOUTI : aucune livraison créée"); sys.exit(2)
    if r and r != seen:
        seen = r
        print("\n".join(r))
    if r and all("|delivered|" in x for x in r):
        sys.exit(0)
    time.sleep(5)
print("non livrée dans le délai"); sys.exit(1)
