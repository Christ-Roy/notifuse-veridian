package domain

import "strings"

// Veridian — throttle par classe de provider destinataire (cold outbound).
//
// Le moteur upstream ne throttle que par intégration émettrice
// (EmailProvider.RateLimitPerMinute). Pour le cold mailing, la délivrabilité
// impose des cadences distinctes par boîte RECEVEUSE (Gmail ≠ Microsoft ≠
// FAI FR ≠ corporate). Ce fichier porte la classification du destinataire et
// la config de débits par classe ; l'enforcement vit dans
// internal/service/queue/veridian_provider_throttle.go.
//
// Contrat cross-app (figé avec l'agent tunnel-de-vente, ticket
// todo/2026-06-02-throttle-par-provider-destinataire.md) :
//   - valeurs canoniques : google | microsoft | yahoo_aol | freemail_fr | corporate
//   - tag optionnel posé par l'export Prospection sur le contact :
//     custom_string_5 (court-circuit option B ; fallback = classification locale)
//   - config par broadcast : broadcast.metadata["veridian_provider_class_rates"]
//   - défauts workspace : workspace settings veridian_provider_class_rates
//   - débits en emails/minute, fractions autorisées (0.5 = 1 mail / 2 min)

// Classes canoniques de provider destinataire.
const (
	ProviderClassGoogle     = "google"
	ProviderClassMicrosoft  = "microsoft"
	ProviderClassYahooAol   = "yahoo_aol"
	ProviderClassFreemailFR = "freemail_fr"
	ProviderClassCorporate  = "corporate"
)

// VeridianProviderClassRatesMetadataKey est la clé de broadcast.Metadata
// portant la map {classe: emails/minute} pour ce broadcast.
const VeridianProviderClassRatesMetadataKey = "veridian_provider_class_rates"

// VeridianProviderClassDailyCapMetadataKey est la clé de broadcast.Metadata
// portant la map {classe: emails/jour MAX} pour ce broadcast. Plafond
// JOURNALIER durable (≠ le débit par minute des rates) : protège la réputation
// d'envoi en limitant le volume quotidien vers une classe receveuse entière.
// 0 ou absent = pas de plafond journalier (le débit minute reste appliqué).
const VeridianProviderClassDailyCapMetadataKey = "veridian_provider_class_daily_cap"

// VeridianPerRecipientDailyCapMetadataKey est la clé de broadcast.Metadata
// portant le plafond JOURNALIER d'envois vers une MÊME adresse (anti-harcèlement
// du même contact). Entier global (non keyé par classe). 0 ou absent = illimité.
const VeridianPerRecipientDailyCapMetadataKey = "veridian_per_recipient_daily_cap"

// veridianProviderClassSet permet la validation O(1) d'une valeur canonique.
var veridianProviderClassSet = map[string]struct{}{
	ProviderClassGoogle:     {},
	ProviderClassMicrosoft:  {},
	ProviderClassYahooAol:   {},
	ProviderClassFreemailFR: {},
	ProviderClassCorporate:  {},
}

// veridianProviderDomainTable mappe les domaines destinataires connus vers
// leur classe (V1 : table statique de suffixes, zéro I/O — la résolution MX
// des domaines corporate hébergés chez Google/M365 est faite en amont par
// l'export Prospection via le tag contact).
var veridianProviderDomainTable = map[string]string{
	// Google grand public
	"gmail.com":      ProviderClassGoogle,
	"googlemail.com": ProviderClassGoogle,

	// Microsoft grand public
	"outlook.com":     ProviderClassMicrosoft,
	"outlook.fr":      ProviderClassMicrosoft,
	"outlook.be":      ProviderClassMicrosoft,
	"outlook.de":      ProviderClassMicrosoft,
	"outlook.es":      ProviderClassMicrosoft,
	"outlook.it":      ProviderClassMicrosoft,
	"outlook.co.uk":   ProviderClassMicrosoft,
	"hotmail.com":     ProviderClassMicrosoft,
	"hotmail.fr":      ProviderClassMicrosoft,
	"hotmail.be":      ProviderClassMicrosoft,
	"hotmail.de":      ProviderClassMicrosoft,
	"hotmail.es":      ProviderClassMicrosoft,
	"hotmail.it":      ProviderClassMicrosoft,
	"hotmail.co.uk":   ProviderClassMicrosoft,
	"live.com":        ProviderClassMicrosoft,
	"live.fr":         ProviderClassMicrosoft,
	"live.be":         ProviderClassMicrosoft,
	"live.co.uk":      ProviderClassMicrosoft,
	"msn.com":         ProviderClassMicrosoft,
	"windowslive.com": ProviderClassMicrosoft,

	// Yahoo / AOL
	"yahoo.com":      ProviderClassYahooAol,
	"yahoo.fr":       ProviderClassYahooAol,
	"yahoo.co.uk":    ProviderClassYahooAol,
	"yahoo.de":       ProviderClassYahooAol,
	"yahoo.es":       ProviderClassYahooAol,
	"yahoo.it":       ProviderClassYahooAol,
	"ymail.com":      ProviderClassYahooAol,
	"rocketmail.com": ProviderClassYahooAol,
	"aol.com":        ProviderClassYahooAol,
	"aol.fr":         ProviderClassYahooAol,

	// Freemail / FAI français
	"orange.fr":        ProviderClassFreemailFR,
	"wanadoo.fr":       ProviderClassFreemailFR,
	"free.fr":          ProviderClassFreemailFR,
	"sfr.fr":           ProviderClassFreemailFR,
	"neuf.fr":          ProviderClassFreemailFR,
	"laposte.net":      ProviderClassFreemailFR,
	"bbox.fr":          ProviderClassFreemailFR,
	"numericable.fr":   ProviderClassFreemailFR,
	"numericable.com":  ProviderClassFreemailFR,
	"club-internet.fr": ProviderClassFreemailFR,
	"aliceadsl.fr":     ProviderClassFreemailFR,
	"cegetel.net":      ProviderClassFreemailFR,
	"dartybox.com":     ProviderClassFreemailFR,
	"gmx.fr":           ProviderClassFreemailFR,
	"9online.fr":       ProviderClassFreemailFR,
	"noos.fr":          ProviderClassFreemailFR,
}

// IsValidProviderClass retourne true si s est une classe canonique.
func IsValidProviderClass(s string) bool {
	_, ok := veridianProviderClassSet[s]
	return ok
}

// VeridianDomainsForClass retourne la liste des domaines connus d'une classe et
// un booléen `exclude`. Pour les classes adossées à une table de suffixes
// (google/microsoft/yahoo_aol/freemail_fr), `exclude=false` et la liste contient
// leurs domaines. Pour `corporate` (= tout domaine inconnu), `exclude=true` et
// la liste contient TOUS les domaines connus à exclure. Classe inconnue → liste
// vide, exclude=false (aucun domaine ne matche). Sert au COUNT par classe du
// plafond journalier (la classe n'est pas matérialisée en DB, on la dérive par
// domaines — cf. CountSentSinceForDomains).
func VeridianDomainsForClass(class string) (domains []string, exclude bool) {
	if class == ProviderClassCorporate {
		all := make([]string, 0, len(veridianProviderDomainTable))
		for d := range veridianProviderDomainTable {
			all = append(all, d)
		}
		return all, true
	}
	if !IsValidProviderClass(class) {
		return nil, false
	}
	matched := make([]string, 0, 16)
	for d, c := range veridianProviderDomainTable {
		if c == class {
			matched = append(matched, d)
		}
	}
	return matched, false
}

// ClassifyProviderClass dérive la classe de provider destinataire depuis
// l'adresse email (V1 : suffixe de domaine). Fallback corporate pour tout
// domaine inconnu, email invalide ou vide — jamais d'erreur, jamais de panic.
func ClassifyProviderClass(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ProviderClassCorporate
	}
	domain := strings.ToLower(strings.TrimSpace(email[at+1:]))
	// FQDN absolu : "gmail.com." est strictement équivalent à "gmail.com" en
	// DNS. On normalise le point terminal pour ne pas mal classer un Gmail
	// présenté en FQDN absolu (rare mais légal) en corporate.
	domain = strings.TrimSuffix(domain, ".")
	if class, ok := veridianProviderDomainTable[domain]; ok {
		return class
	}
	return ProviderClassCorporate
}

// VeridianProviderClassRatesFromMetadata extrait la map {classe: emails/minute}
// d'un broadcast.Metadata. Seules les classes canoniques avec un débit
// strictement positif sont retenues ; tout le reste est ignoré silencieusement
// (une config malformée ne doit jamais bloquer un envoi — elle dégrade vers
// "pas de throttle classe", le throttle émetteur restant appliqué).
func VeridianProviderClassRatesFromMetadata(metadata MapOfAny) map[string]float64 {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[VeridianProviderClassRatesMetadataKey]
	if !ok {
		return nil
	}

	rates := make(map[string]float64)
	switch m := raw.(type) {
	case map[string]any:
		for class, v := range m {
			if !IsValidProviderClass(class) {
				continue
			}
			if rate, ok := veridianToFloat(v); ok && rate > 0 {
				rates[class] = rate
			}
		}
	case map[string]float64:
		for class, rate := range m {
			if IsValidProviderClass(class) && rate > 0 {
				rates[class] = rate
			}
		}
	}

	if len(rates) == 0 {
		return nil
	}
	return rates
}

// VeridianProviderClassDailyCapFromMetadata extrait la map {classe: cap/jour}
// d'un broadcast.Metadata. Seules les classes canoniques avec un cap entier
// strictement positif sont retenues (un cap <= 0 = pas de plafond, on l'ignore
// donc plutôt que de bloquer tout envoi). Config malformée → nil = pas de cap
// journalier classe, comme l'extraction des rates (dégradation gracieuse).
func VeridianProviderClassDailyCapFromMetadata(metadata MapOfAny) map[string]int {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[VeridianProviderClassDailyCapMetadataKey]
	if !ok {
		return nil
	}

	caps := make(map[string]int)
	switch m := raw.(type) {
	case map[string]any:
		for class, v := range m {
			if !IsValidProviderClass(class) {
				continue
			}
			if cap, ok := veridianToInt(v); ok && cap > 0 {
				caps[class] = cap
			}
		}
	case map[string]int:
		for class, cap := range m {
			if IsValidProviderClass(class) && cap > 0 {
				caps[class] = cap
			}
		}
	case map[string]float64:
		for class, cap := range m {
			if IsValidProviderClass(class) && cap > 0 {
				caps[class] = int(cap)
			}
		}
	}

	if len(caps) == 0 {
		return nil
	}
	return caps
}

// VeridianPerRecipientDailyCapFromMetadata extrait le plafond journalier par
// destinataire d'un broadcast.Metadata. Retourne 0 si absent, malformé ou <= 0
// (0 = illimité, sémantique opt-in).
func VeridianPerRecipientDailyCapFromMetadata(metadata MapOfAny) int {
	if metadata == nil {
		return 0
	}
	raw, ok := metadata[VeridianPerRecipientDailyCapMetadataKey]
	if !ok {
		return 0
	}
	if cap, ok := veridianToInt(raw); ok && cap > 0 {
		return cap
	}
	return 0
}

// veridianToInt normalise les types numériques possibles après un round-trip
// JSON (float64) ou une construction Go directe (int). Les valeurs
// fractionnaires sont tronquées (un cap journalier est un entier).
func veridianToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}

// veridianToFloat normalise les types numériques possibles après un
// round-trip JSON (float64) ou une construction Go directe (int).
func veridianToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// VeridianContactProviderClass lit le tag provider_class posé par l'export
// batch en amont (contrat : custom_string_5). Retourne "" si absent ou
// non-canonique — l'appelant retombe alors sur ClassifyProviderClass.
func VeridianContactProviderClass(contact *Contact) string {
	if contact == nil || contact.CustomString5 == nil || contact.CustomString5.IsNull {
		return ""
	}
	class := strings.ToLower(strings.TrimSpace(contact.CustomString5.String))
	if !IsValidProviderClass(class) {
		return ""
	}
	return class
}

// VeridianApplyProviderThrottle enrichit une entrée de queue avec la config de
// throttle par classe au moment de l'enqueue : tag contact (option B) +
// débits du broadcast (metadata). Sans config ni tag, l'entrée reste
// strictement identique à l'upstream (non-régression). Le worker complète :
// classification locale si tag absent, défauts workspace si le broadcast ne
// définit pas de débits.
func VeridianApplyProviderThrottle(entry *EmailQueueEntry, broadcast *Broadcast, contact *Contact) {
	if entry == nil {
		return
	}
	if class := VeridianContactProviderClass(contact); class != "" {
		entry.Payload.VeridianProviderClass = class
	}
	if broadcast != nil {
		if rates := VeridianProviderClassRatesFromMetadata(broadcast.Metadata); len(rates) > 0 {
			entry.Payload.VeridianProviderClassRates = rates
		}
		if caps := VeridianProviderClassDailyCapFromMetadata(broadcast.Metadata); len(caps) > 0 {
			entry.Payload.VeridianProviderClassDailyCap = caps
		}
		if cap := VeridianPerRecipientDailyCapFromMetadata(broadcast.Metadata); cap > 0 {
			entry.Payload.VeridianPerRecipientDailyCap = cap
		}
	}
}
