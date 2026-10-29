// Données d'essai réalistes de la boîte unifiée (tests de composants et captures hors ligne).
// Jamais importé par le code de l'application. Aucune adresse réelle : domaines d'exemple.

import type { Mailbox, MailMessage, MailSummary, MailThread } from '../../services/api/veridian_mail'

export const BANNER_PNG_BASE64 = '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'
export const FACADE_PNG_BASE64 = 'iVBORw0KGgoAAAANSUhEUgAAAUAAAADICAIAAAAWZq/8AAAD/klEQVR42u3cOW7UYBzG4XHkS1CRQICeO3CAOQOERSlJwwloSIlYAmeYhj3Qs4YSkYSt4hpUSJFmWKzY4++1n6dLFceen/7+4s9TPXr9aQJkWnEKQMCAgAEBg4ABAQMCBubVlXMAJjDQwwSeGMGD9uPa9OSdmfMwVNXjN7ZSDjbdoz/KWMBEpitjAROfrowFTHy6MhYw8enKeDgBPxFwpu/H63beqpIFTGK6MhYw8enKWMDEpyvjtIDfCrjsdK9O+z2A1bsyFjCB6cpYwMSnK2MBE5+ujAsN+KmAy/AtId2j1mQsYBLTlbGAye5WyQKW7gDJWMDSlTEClq6MRxHwOwF3nO6V6Zj//LV7MhawdGXMwoCfCbgDX6W7yCkZC1i6MkbA0pWxgJGujAWsW5QsYOnKWMBIV8YCli4yXkbAz98L+B++XJZub07fl7GApStjAUsXGQtYushYwNKVMQKWrowFLF1kLGDdouSmAb8YWcCH0h269TFlPKKApSvjIQb8YfgBH25Id6wZ78wELF1kLGDpImMBSxcZDzNg6TK2jAcSsHQZZ8bVbnjAB9LleM4kZ5wasG5RcmTA0kXGkQFLFxnPBbwXEPDBJenSR8YPZgKWLjIeX8DSRcaRAUsXGf9vwC9LCnhfuiQ4W0zGpQQsXWQcGbB0kXFewLpFyZEBSxcZRwYsXWTcdsAflxHw/kXpMr6MH87iA5YuMu4w4FedBfxZuvDbuW4y7jDgv9u9ueGiMiQXbuws/5euOO+QS8AgYEDAgIBBwEDx6qpyEqAFvaRkAoNbaEDAgIBBwICAAQEDC9QeA0MreknJBIbkCTwxgiF2BJvAEEzAIGBAwEAjdeJBX799q8wD297c8pFyBZcasH9CQys8BwYEDAIGAtbAdmJB7iLYBAa30ICAgWZrYEtgiF0Cm8DgFhoQMCBgEDBQPjuxoCV2YgHNJrABDLED2ASGZAIGAQMCBgQMAgYEDHSnrjwILs/e4c8yD+z8+glX508qO7EAAYOAAQEDAgYEDAIGCuF9YGiH94GBhhPYd2JB7gg2gSGYgEHAgIABAcNYeA4M7fAcGBAwCBgIWAPbiQW5i+A68URtb275tERzBd1CAwKG6DWwJTDELoFNYHALDQgYEDAIGCifnVjQEt+JBTSbwAYwxA5gExiSCRgEDAgYEDAIGBAw0J268iAY2lDZiQUIGAQMCBgQMCBgEDBQCO8DQzu8Dww0nMC+EwtyR7AJDMEEDAIGBAwIGMbCc2Boh+fAgIBBwEDAGthOLMhdBJvA4BYaEDDQbA1sCQyxS2ATGNxCAwIGBAwCBspnJxa0xE4soNkENoAhdgCbwJBMwCBgQMCAgEHAgICB7vwCmLxkhAneCl4AAAAASUVORK5CYII='

const iso = (base: number, minutesAgo: number): string => new Date(base - minutesAgo * 60_000).toISOString()

export const WS = 'ws-1'

export const MAILBOXES: Mailbox[] = [
  {
    id: 'prof-nord',
    address: 'contact@asd-nord.example',
    profile_id: 'prof-nord',
    profile_name: 'ASD Nord',
    integration_id: 'imap-nord',
    folder: 'INBOX',
    count_in: 41,
    count_out: 612,
    count_unhandled: 3,
    last_message_at: new Date().toISOString(),
    sync: { uid_validity: 1, last_uid: 903, last_ok_at: new Date().toISOString(), total_ingested: 41 }
  },
  {
    id: 'prof-sud',
    address: 'bonjour@asd-sud.example',
    profile_id: 'prof-sud',
    profile_name: 'ASD Sud',
    integration_id: 'imap-sud',
    folder: 'INBOX',
    count_in: 18,
    count_out: 405,
    count_unhandled: 1,
    sync: { uid_validity: 1, last_uid: 311, last_ok_at: new Date().toISOString(), total_ingested: 18 }
  }
]

export const RICH_HTML = `<!doctype html><html><head><style>
.card{border:1px solid #d9d9d9;border-radius:8px;padding:12px 16px;margin:12px 0}
.btn{display:inline-block;background:#1f5c42;color:#ffffff;padding:8px 14px;border-radius:6px;text-decoration:none}
</style></head><body>
<p>Bonjour Robert,</p>
<p>Merci pour votre message. Votre proposition m'intéresse : nous cherchons justement un fournisseur pour la <b>gamme de compléments articulaires</b> de la clinique.</p>
<div class="card"><table style="border-collapse:collapse;width:100%">
<tr><th align="left" style="border-bottom:1px solid #ccc;padding:4px 8px">Produit</th><th align="right" style="border-bottom:1px solid #ccc;padding:4px 8px">Quantité / mois</th></tr>
<tr><td style="padding:4px 8px">Complément articulaire chien 120 g</td><td align="right" style="padding:4px 8px">40</td></tr>
<tr><td style="padding:4px 8px">Spray cicatrisant 200 ml</td><td align="right" style="padding:4px 8px">25</td></tr>
</table></div>
<p>Pouvez-vous m'envoyer le <a href="https://www.exemple.fr/catalogue-pro">catalogue professionnel</a> et vos conditions de livraison ? Nous pouvons en parler jeudi, voici la <a class="btn" href="https://agenda.exemple.fr/rdv/martin">prise de rendez-vous</a>.</p>
<p>Cordialement,<br>Dr Jean Martin<br><img src="cid:banniere@martin" alt="Clinique Martin" width="360" height="110"></p>
<img src="https://suivi.exemple.fr/o/pixel.gif" width="1" height="1" alt="">
<blockquote>Le 26 oct. 2026 à 09:12, ASD a écrit :<br>Bonjour Docteur, nous fabriquons des compléments pour animaux de clinique…</blockquote>
</body></html>`

export const PLAIN_REPLY_TEXT = `Bonjour,

Merci, pas pour le moment. Voyez avec ma collègue : https://www.exemple.fr/contact.

Le 26 oct. 2026 à 09:12, ASD a écrit :
> Bonjour Docteur,
> nous fabriquons des compléments pour animaux de clinique.
> Seriez-vous disponible pour un échange ?
`

let seq = 100

export function makeMessage(over: Partial<MailMessage> & Pick<MailMessage, 'id' | 'kind' | 'direction'>): MailMessage {
  seq += 1
  const at = over.occurred_at ?? new Date().toISOString()
  return {
    seq,
    thread_id: 'th-1',
    contact_email: 'jean.martin@clinique-martin.example',
    from: 'Dr Jean Martin <jean.martin@clinique-martin.example>',
    from_name: 'Dr Jean Martin',
    to: ['contact@asd-nord.example'],
    subject: 'Re: Compléments articulaires pour votre clinique',
    occurred_at: at,
    received_at: over.direction === 'in' ? at : undefined,
    sent_at: over.direction === 'out' ? at : undefined,
    profile_id: 'prof-nord',
    body_source: over.direction === 'in' ? 'received' : 'captured',
    created_at: at,
    updated_at: at,
    ...over
  }
}

/** Fil complet : premier mail, relance, réponse humaine riche avec pièces jointes. */
export function richThread(base = Date.now()): { thread: MailThread; reply: MailMessage; first: MailMessage; relance: MailMessage } {
  const first = makeMessage({
    id: 'm-first',
    kind: 'sent',
    direction: 'out',
    occurred_at: iso(base, 60 * 24 * 3 + 120),
    from: 'ASD Nord <contact@asd-nord.example>',
    from_name: 'ASD Nord',
    to: ['jean.martin@clinique-martin.example'],
    subject: 'Compléments articulaires pour votre clinique',
    body_html_sanitized:
      '<p>Bonjour Docteur,</p><p>Nous fabriquons des compléments pour animaux, utilisés dans plus de 200 cliniques. Seriez-vous disponible pour un échange de dix minutes ?</p><p>Bien cordialement,<br>L’équipe ASD</p>',
    body_text: 'Bonjour Docteur,\n\nNous fabriquons des compléments pour animaux. Seriez-vous disponible pour un échange de dix minutes ?\n\nBien cordialement,\nL’équipe ASD',
    link: { sent_message_id: 'sent-1', automation_id: 'seq-cliniques', node_id: 'j0-email', match_type: 'message_id' }
  })
  const relance = makeMessage({
    id: 'm-relance',
    kind: 'sent',
    direction: 'out',
    occurred_at: iso(base, 60 * 24 * 1 + 120),
    from: 'ASD Nord <contact@asd-nord.example>',
    from_name: 'ASD Nord',
    to: ['jean.martin@clinique-martin.example'],
    subject: 'Re: Compléments articulaires pour votre clinique',
    body_text: 'Bonjour Docteur,\n\nJe me permets de revenir vers vous au sujet de mon message de lundi. Un échange rapide cette semaine ?\n\nBien cordialement',
    link: { sent_message_id: 'sent-2', automation_id: 'seq-cliniques', node_id: 'j3-relance', match_type: 'message_id' }
  })
  const reply = makeMessage({
    id: 'm-reply',
    kind: 'reply_human',
    direction: 'in',
    occurred_at: iso(base, 95),
    body_html_sanitized: RICH_HTML,
    body_text: 'Bonjour Robert,\n\nMerci pour votre message. Votre proposition m’intéresse.\n\n> Bonjour Docteur,\n> nous fabriquons des compléments…',
    headers: {
      From: ['Dr Jean Martin <jean.martin@clinique-martin.example>'],
      To: ['contact@asd-nord.example'],
      Subject: ['Re: Compléments articulaires pour votre clinique'],
      'Message-ID': ['<CAF3a9x2@mail.clinique-martin.example>'],
      'In-Reply-To': ['<relance-2@asd-nord.example>'],
      'Content-Type': ['multipart/mixed; boundary="----=_Part_1"'],
      Received: ['from mail.clinique-martin.example by relais-1.asd-nord.example with ESMTPS']
    },
    attachments: [
      { index: 0, name: 'banniere.png', content_type: 'image/png', size: 7603, inline: true, content_id: '<banniere@martin>', available: true },
      { index: 1, name: 'catalogue-2026-clinique-martin.pdf', content_type: 'application/pdf', size: 2_340_000, available: true },
      { index: 2, name: 'facade-clinique.png', content_type: 'image/png', size: 1079, available: true },
      { index: 3, name: 'tarifs.xlsx', content_type: 'application/octet-stream', size: 48_200, available: true }
    ],
    labels: ['chaud', 'catalogue'],
    link: { sent_message_id: 'sent-2', automation_id: 'seq-cliniques', node_id: 'j3-relance', match_type: 'message_id' },
    size_bytes: 2_400_000
  })
  const thread: MailThread = {
    thread_id: 'th-1',
    subject: first.subject,
    contact_email: first.contact_email,
    first_at: first.occurred_at,
    last_at: reply.occurred_at,
    message_count: 3,
    last_kind: 'reply_human',
    messages: [first, relance, reply],
    milestones: [
      { id: first.id, label: 'Premier mail', kind: 'sent', at: first.occurred_at },
      { id: relance.id, label: 'Relance 1', kind: 'sent', at: relance.occurred_at },
      { id: reply.id, label: 'Réponse', kind: 'reply_human', at: reply.occurred_at }
    ]
  }
  return { thread, reply, first, relance }
}

/** Rebond définitif avec rapport de non-remise décodé. */
export function hardBounce(base = Date.now()): MailMessage {
  return makeMessage({
    id: 'm-bounce',
    kind: 'bounce_hard',
    direction: 'in',
    thread_id: 'th-2',
    contact_email: 'accueil@vetos-du-lac.example',
    from: 'Mail Delivery Subsystem <mailer-daemon@googlemail.com>',
    from_name: 'Mail Delivery Subsystem',
    to: ['contact@asd-nord.example'],
    subject: 'Delivery Status Notification (Failure)',
    occurred_at: iso(base, 40),
    body_text: 'Votre message n’a pas pu être remis à accueil@vetos-du-lac.example.\n\nLa réponse du serveur distant est :\n550 5.1.1 The email account that you tried to reach does not exist.',
    dsn: {
      code: '5.1.1',
      severity: 'hard',
      action: 'failed',
      recipient: 'accueil@vetos-du-lac.example',
      provider: 'Gmail',
      reason_fr: 'L’adresse n’existe pas chez le fournisseur du destinataire.',
      advice_fr: 'Ce contact est retiré des séquences : l’adresse est morte, inutile d’insister. Vérifiez la source de l’adresse.',
      category: 'adresse_inexistante',
      diagnostic: '550-5.1.1 The email account that you tried to reach does not exist. Please try\n550-5.1.1 double-checking the recipient\'s email address for typos or\n550-5.1.1 unnecessary spaces. Learn more at\n550 5.1.1 https://support.google.com/mail/?p=NoSuchUser x7-20020a0c1234si1234567qvh.88 - gsmtp'
    },
    link: { sent_message_id: 'sent-9', automation_id: 'seq-cliniques', node_id: 'j0-email', match_type: 'message_id' },
    headers: { 'Content-Type': ['multipart/report; report-type=delivery-status'], 'Auto-Submitted': ['auto-replied'] }
  })
}

export function softBounce(base = Date.now()): MailMessage {
  return makeMessage({
    id: 'm-soft',
    kind: 'bounce_soft',
    direction: 'in',
    thread_id: 'th-4',
    contact_email: 'dr.petit@cabinet-petit.example',
    from: 'postmaster@cabinet-petit.example',
    subject: 'Undeliverable: Compléments articulaires pour votre clinique',
    occurred_at: iso(base, 200),
    body_text: 'Boîte pleine.',
    dsn: {
      code: '4.2.2',
      severity: 'soft',
      action: 'failed',
      recipient: 'dr.petit@cabinet-petit.example',
      provider: 'Microsoft',
      reason_fr: 'La boîte du destinataire est pleine pour le moment.',
      advice_fr: 'Le mail sera retenté plus tard : rien à faire, le contact reste dans la séquence.',
      category: 'boite_pleine',
      diagnostic: '452 4.2.2 The email account that you tried to reach is over quota.'
    }
  })
}

export function autoReply(base = Date.now()): MailMessage {
  return makeMessage({
    id: 'm-auto',
    kind: 'reply_auto',
    direction: 'in',
    thread_id: 'th-3',
    contact_email: 'secretariat@clinique-du-parc.example',
    from: 'Secrétariat Clinique du Parc <secretariat@clinique-du-parc.example>',
    subject: 'Réponse automatique : Compléments articulaires pour votre clinique',
    occurred_at: iso(base, 15),
    body_text: 'Bonjour,\n\nNous sommes fermés du 28 octobre au 3 novembre. En cas d’urgence vétérinaire, appelez le 3115 vétérinaire de garde.\n\nCordialement'
  })
}

export function summaryOf(m: MailMessage, preview?: string): MailSummary {
  return {
    id: m.id,
    seq: m.seq,
    direction: m.direction,
    kind: m.kind,
    profile_id: m.profile_id,
    thread_id: m.thread_id,
    contact_email: m.contact_email,
    from: m.from,
    to: m.to,
    subject: m.subject,
    preview: preview ?? (m.body_text ?? '').split('\n').filter((l) => !l.trim().startsWith('>')).join(' ').replace(/\s+/g, ' ').slice(0, 140),
    occurred_at: m.occurred_at,
    has_attachments: (m.attachments ?? []).some((a) => !(a.inline && a.content_id)),
    attachment_count: (m.attachments ?? []).length,
    dsn_reason_fr: m.dsn?.reason_fr,
    handled_at: m.handled_at,
    handled_by: m.handled_by,
    labels: m.labels
  }
}

export function inboxFixture(base = Date.now()) {
  const { thread, reply, first, relance } = richThread(base)
  const bounce = hardBounce(base)
  const soft = softBounce(base)
  const auto = autoReply(base)
  const other = makeMessage({
    id: 'm-human-2',
    kind: 'reply_human',
    direction: 'in',
    thread_id: 'th-5',
    contact_email: 'clinique.verte@exemple.example',
    from: 'Camille Roux <clinique.verte@exemple.example>',
    from_name: 'Camille Roux',
    subject: 'Re: Compléments articulaires pour votre clinique',
    occurred_at: iso(base, 60 * 26),
    body_text: PLAIN_REPLY_TEXT,
    handled_at: iso(base, 60 * 20),
    handled_by: 'humain:42'
  })
  const messages = [reply, auto, bounce, soft, other]
  const threads: Record<string, MailThread> = {
    'th-1': thread,
    'th-2': { thread_id: 'th-2', subject: bounce.subject, contact_email: bounce.contact_email, first_at: iso(base, 70), last_at: bounce.occurred_at, message_count: 2, last_kind: 'bounce_hard', messages: [
      makeMessage({ id: 'm-b-sent', kind: 'sent', direction: 'out', thread_id: 'th-2', contact_email: bounce.contact_email, from: 'ASD Nord <contact@asd-nord.example>', to: [bounce.contact_email], subject: 'Compléments articulaires pour votre clinique', occurred_at: iso(base, 70), body_text: 'Bonjour,\n\nNous fabriquons des compléments pour animaux.' }),
      bounce
    ], milestones: [
      { id: 'm-b-sent', label: 'Premier mail', kind: 'sent', at: iso(base, 70) },
      { id: bounce.id, label: 'Rebond définitif', kind: 'bounce_hard', at: bounce.occurred_at }
    ] }
  }
  return { thread, reply, first, relance, bounce, soft, auto, other, messages, summaries: messages.map((m) => summaryOf(m)), threads }
}
