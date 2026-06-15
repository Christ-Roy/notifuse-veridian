package domain

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Veridian — classification du provider destinataire par MX RÉEL (Option A,
// Lot 4, ticket todo/2026-06-14-classification-mx-table-patterns-option-A.md).
//
// LE PROBLÈME : la classification par SUFFIXE de domaine (ClassifyProviderClass)
// jette ~70% des leads B2B en `corporate` alors qu'une grande part est en
// réalité hébergée Google Workspace / Microsoft 365 / OVH / IONOS. Leur envoyer
// du cold au débit `corporate` (rapide) = taper Google/Microsoft sans throttle =
// réputation grillée à grande échelle.
//
// LA VÉRITÉ universelle = le MX record du domaine (le DNS dit où le mail
// atterrit, peu importe le nom de domaine). On :
//   1. tente le suffixe connu d'abord (gmail.com/orange.fr → classe directe,
//      ZÉRO I/O, hot path rapide) ;
//   2. pour un suffixe INCONNU : on résout le MX (resolver fiable 8.8.8.8 /
//      1.1.1.1, timeout court) et on mappe le HOSTNAME MX à une classe via une
//      table de patterns versionnée (case-insensitive, match sur le SUFFIXE du
//      hostname MX — données d'infra STABLES : Google = *.google.com depuis 15 ans) ;
//   3. fallback `corporate_selfhost` (débit prudent) pour l'inconnu / l'échec /
//      le timeout — best-effort STRICT : un lookup lent ne bloque JAMAIS l'envoi.
//
// CACHE : in-memory, clé domaine→classe, TTL long (les MX changent rarement).
// Choix assumé d'un cache MÉMOIRE plutôt qu'une table DB :
//   - les MX sont une donnée d'infra GLOBALE (pas par workspace) → une table
//     par workspace serait sémantiquement fausse, et le modèle multi-tenant de
//     Notifuse n'a pas de DB « globale » naturelle ;
//   - le process worker est long-lived → le cache survit à tout un batch et
//     bien au-delà ; un domaine résolu une fois ne re-résout pas avant le TTL ;
//   - zéro migration, zéro schéma, zéro couplage tenant. Pré-remplissage le plus
//     performant pour 7,8M = le tag contact custom_string_5 (override option B,
//     posé à l'import par Prospection) qui court-circuite tout lookup à l'envoi.
//   Si demain un cache PARTAGÉ inter-process devient nécessaire (plusieurs
//   workers, churn de containers), on matérialisera une table de cache globale —
//   pas avant d'en mesurer le besoin (même discipline que le daily cap).

// veridianMXLookupTimeout borne un lookup MX. Best-effort : passé ce délai on
// dégrade vers corporate_selfhost sans jamais bloquer l'envoi.
const veridianMXLookupTimeout = 2 * time.Second

// veridianMXCacheTTL est la durée de vie d'une entrée de cache domaine→classe.
// Les MX d'un domaine changent très rarement (migration de provider = événement
// rare) ; 7 jours est largement prudent et évite de marteler le DNS.
const veridianMXCacheTTL = 7 * 24 * time.Hour

// MXResolver abstrait la résolution MX pour permettre l'injection d'un faux
// resolver en test (zéro dépendance DNS réelle). L'implémentation de prod
// (veridianNetMXResolver) tape un resolver public fiable avec timeout.
type MXResolver interface {
	// LookupMXHosts retourne la liste des hostnames MX du domaine (sans le
	// point terminal, lowercase de préférence — le matching relowercase de
	// toute façon). Une erreur (NXDOMAIN, timeout, pas de MX) → l'appelant
	// dégrade vers corporate_selfhost.
	LookupMXHosts(ctx context.Context, domain string) ([]string, error)
}

// veridianMXPatternEntry = un pattern de suffixe de hostname MX → classe.
// Le matching est case-INSENSITIVE et sur SUFFIXE (strings.HasSuffix), pour
// absorber les variantes vues en data réelle (ASPMX.L.GOOGLE.com, mx1.mail.ovh.net…).
type veridianMXPatternEntry struct {
	suffix string // suffixe de hostname MX, lowercase
	class  string
}

// veridianMXPatternTable : table de patterns MX→classe VERSIONNÉE, dérivée de la
// VRAIE data (odh-scrape-db.email_verification 48k vérifiés + prospection prod
// 286k, cf. docs/PROVIDERS-DESTINATAIRES-CARTOGRAPHIE.md). ORDRE SIGNIFICATIF :
// les patterns les plus SPÉCIFIQUES (les plus longs / les gateways) d'abord, le
// matching s'arrête au premier suffixe qui matche. Un security_gateway peut
// porter un nom générique : il doit être testé AVANT les hébergeurs génériques.
//
// Pour étendre : ajouter une ligne avec le suffixe lowercase observé en data.
// NE PAS deviner — ne mettre que des suffixes vus dans la cartographie.
var veridianMXPatternTable = []veridianMXPatternEntry{
	// --- Passerelles anti-spam pro (testées EN PREMIER : elles fronton un MX
	// d'entreprise, le débit doit être ULTRA-prudent / quasi-exclusion cold). ---
	{".vadesecure.com", ProviderClassSecurityGateway},
	{".vadesecure.net", ProviderClassSecurityGateway},
	{".mailinblack.com", ProviderClassSecurityGateway},
	{".proofpoint.com", ProviderClassSecurityGateway},
	{".pphosted.com", ProviderClassSecurityGateway}, // Proofpoint hosted
	{".mimecast.com", ProviderClassSecurityGateway},
	{".mimecast.co.za", ProviderClassSecurityGateway},
	{".hornetsecurity.com", ProviderClassSecurityGateway},
	{".securemail.pro", ProviderClassSecurityGateway},
	{".security-mail.net", ProviderClassSecurityGateway},
	{".sophos.com", ProviderClassSecurityGateway},
	{".retarus.com", ProviderClassSecurityGateway},
	{".trendmicro.com", ProviderClassSecurityGateway},
	{".trendmicro.eu", ProviderClassSecurityGateway},
	{".messagelabs.com", ProviderClassSecurityGateway}, // Symantec/Broadcom email security
	{".barracudanetworks.com", ProviderClassSecurityGateway},
	{".cudasvc.com", ProviderClassSecurityGateway}, // Barracuda
	{".altospam.com", ProviderClassSecurityGateway},

	// --- Google (Gmail public + Workspace). ---
	{".google.com", ProviderClassGoogle},     // aspmx.l.google.com, alt*.aspmx.l.google.com, smtp.google.com
	{".googlemail.com", ProviderClassGoogle}, // gmail-smtp-in.l.googlemail.com (variante)
	{".psmtp.com", ProviderClassGoogle},      // ancien Postini (Google)

	// --- Microsoft 365 / Outlook. ---
	{".protection.outlook.com", ProviderClassMicrosoft}, // <tenant>.mail.protection.outlook.com, *.olc.protection.outlook.com
	{".outlook.com", ProviderClassMicrosoft},
	{".office365.com", ProviderClassMicrosoft},
	{".microsoft.com", ProviderClassMicrosoft},

	// --- OVH (nébuleuse FR majeure ~17%). ---
	{".ovh.net", ProviderClassOVH}, // mx0/mx1/mx2/mx3/mx4.mail.ovh.net
	{".ovh.com", ProviderClassOVH},

	// --- IONOS / 1&1 (nébuleuse FR ~6%). ---
	{".ionos.fr", ProviderClassIonos},
	{".ionos.com", ProviderClassIonos},
	{".ionos.de", ProviderClassIonos},
	{".ionos.es", ProviderClassIonos},
	{".ionos.co.uk", ProviderClassIonos},
	{".kundenserver.de", ProviderClassIonos}, // 1&1/IONOS historique
	{".1and1.com", ProviderClassIonos},
	{".1und1.de", ProviderClassIonos},

	// --- Yahoo / AOL. ---
	{".yahoodns.net", ProviderClassYahooAol}, // mta*.am0.yahoodns.net

	// --- FAI / freemail français (host MX, pas le suffixe d'adresse). ---
	{".orange.fr", ProviderClassFreemailFR}, // smtp-in.orange.fr
	{".sfr.fr", ProviderClassFreemailFR},
	{".free.fr", ProviderClassFreemailFR}, // mx1.free.fr
	{".laposte.net", ProviderClassFreemailFR},
	{".bbox.fr", ProviderClassFreemailFR},

	// --- iCloud / Apple. ---
	{".mail.icloud.com", ProviderClassAppleICloud}, // mx0*.mail.icloud.com
	{".icloud.com", ProviderClassAppleICloud},
	{".apple.com", ProviderClassAppleICloud},

	// --- Autres hébergeurs propres (other_hoster : débit standard, providers à
	// part entière mais sans politique aussi sensible que Google/MS). ---
	{".infomaniak.ch", ProviderClassOtherHoster}, // mta-gw.infomaniak.ch
	{".infomaniak.com", ProviderClassOtherHoster},
	{".hostinger.com", ProviderClassOtherHoster},
	{".hostinger.fr", ProviderClassOtherHoster},
	{".mail.gandi.net", ProviderClassOtherHoster}, // spool.mail.gandi.net
	{".gandi.net", ProviderClassOtherHoster},
	{".zoho.eu", ProviderClassOtherHoster},
	{".zoho.com", ProviderClassOtherHoster},
	{".zoho.in", ProviderClassOtherHoster},
	{".online.net", ProviderClassOtherHoster}, // Scaleway/Online
	{".scaleway.com", ProviderClassOtherHoster},
	{".titan.email", ProviderClassOtherHoster},
	{".webador.com", ProviderClassOtherHoster},
	{".webmo.fr", ProviderClassOtherHoster},
	{".o2switch.net", ProviderClassOtherHoster},
	{".amen.fr", ProviderClassOtherHoster},
	{".protonmail.ch", ProviderClassOtherHoster}, // Proton (rangé other_hoster, pas de classe dédiée V1)
	{".proton.me", ProviderClassOtherHoster},
	{".mail.com", ProviderClassOtherHoster},
	{".one.com", ProviderClassOtherHoster},
}

// classifyMXHost mappe un hostname MX à une classe via la table de patterns
// (case-insensitive, suffixe, premier match gagne). Retourne ("", false) si
// aucun pattern ne matche → l'appelant tombe en corporate_selfhost.
func classifyMXHost(host string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(h, ".") // hostname MX peut être un FQDN absolu
	if h == "" {
		return "", false
	}
	for _, entry := range veridianMXPatternTable {
		// HasSuffix attrape `aspmx.l.google.com` via `.google.com` ET
		// `smtp.google.com` ; on ajoute le cas où le host EST exactement le
		// suffixe sans le point initial (ex. host = "google.com").
		if strings.HasSuffix(h, entry.suffix) || h == strings.TrimPrefix(entry.suffix, ".") {
			return entry.class, true
		}
	}
	return "", false
}

// classifyMXHosts applique classifyMXHost à une liste de hostnames MX et
// retourne la classe du PREMIER host qui matche un pattern connu. Un domaine
// peut déclarer plusieurs MX ; en pratique ils pointent vers le même provider,
// mais on parcourt dans l'ordre (déjà trié par priorité par le resolver) et on
// prend le premier reconnu. Aucun reconnu → ("", false).
func classifyMXHosts(hosts []string) (string, bool) {
	for _, h := range hosts {
		if class, ok := classifyMXHost(h); ok {
			return class, true
		}
	}
	return "", false
}

// veridianMXCacheEntry = une entrée de cache avec sa date d'expiration.
type veridianMXCacheEntry struct {
	class     string
	expiresAt time.Time
}

// VeridianMXClassifier classe un domaine par MX réel avec cache in-memory et
// best-effort strict. Thread-safe (RWMutex) : un même classifier est partagé
// par tout le worker. Le resolver est injectable pour les tests.
type VeridianMXClassifier struct {
	resolver MXResolver
	ttl      time.Duration

	mu    sync.RWMutex
	cache map[string]veridianMXCacheEntry

	now func() time.Time // injectable pour tester l'expiration du cache
}

// NewVeridianMXClassifier construit un classifier. Si resolver est nil, on
// utilise le resolver réseau par défaut (8.8.8.8 / 1.1.1.1, timeout court).
func NewVeridianMXClassifier(resolver MXResolver) *VeridianMXClassifier {
	if resolver == nil {
		resolver = newVeridianNetMXResolver()
	}
	return &VeridianMXClassifier{
		resolver: resolver,
		ttl:      veridianMXCacheTTL,
		cache:    make(map[string]veridianMXCacheEntry),
		now:      time.Now,
	}
}

// ClassifyEmail classe une adresse email par : (1) tag contact non géré ici —
// l'appelant gère l'override custom_string_5 avant ; (2) suffixe connu → classe
// directe SANS lookup ; (3) suffixe inconnu → MX caché ; (4) fallback
// corporate_selfhost. Best-effort STRICT : jamais d'erreur, jamais de blocage.
func (c *VeridianMXClassifier) ClassifyEmail(ctx context.Context, email string) string {
	domain := veridianDomainFromEmail(email)
	if domain == "" {
		return ProviderClassCorporateSelfhost
	}
	return c.ClassifyDomain(ctx, domain)
}

// ClassifyDomain classe un domaine NORMALISÉ (lowercase, sans point terminal)
// par suffixe puis MX caché. C'est le cœur de la cascade Lot 4.
func (c *VeridianMXClassifier) ClassifyDomain(ctx context.Context, domain string) string {
	if domain == "" {
		return ProviderClassCorporateSelfhost
	}

	// 1. Suffixe connu (grand public) → classe directe, ZÉRO I/O. Hot path.
	if class, ok := classifyBySuffix(domain); ok {
		return class
	}

	// 2. Cache hit ? (un domaine custom déjà résolu ne re-résout pas avant le TTL)
	if class, ok := c.cacheGet(domain); ok {
		return class
	}

	// 3. Résolution MX, bornée par timeout. Best-effort : tout échec dégrade.
	class := c.resolveMX(ctx, domain)

	// 4. Mémorise le résultat (y compris le fallback corporate_selfhost : un
	//    domaine sans MX reconnu ne doit pas re-déclencher un lookup à chaque
	//    envoi — il restera corporate_selfhost jusqu'au TTL).
	c.cacheSet(domain, class)
	return class
}

// resolveMX fait le lookup MX (avec timeout) et mappe le résultat. Best-effort
// STRICT : NXDOMAIN / timeout / pas de MX / aucun pattern reconnu →
// corporate_selfhost. Ne retourne JAMAIS d'erreur (le contrat throttle Veridian
// interdit de bloquer un envoi sur une lecture).
func (c *VeridianMXClassifier) resolveMX(ctx context.Context, domain string) string {
	lookupCtx, cancel := context.WithTimeout(ctx, veridianMXLookupTimeout)
	defer cancel()

	hosts, err := c.resolver.LookupMXHosts(lookupCtx, domain)
	if err != nil || len(hosts) == 0 {
		return ProviderClassCorporateSelfhost
	}
	if class, ok := classifyMXHosts(hosts); ok {
		return class
	}
	return ProviderClassCorporateSelfhost
}

func (c *VeridianMXClassifier) cacheGet(domain string) (string, bool) {
	c.mu.RLock()
	entry, ok := c.cache[domain]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}
	if c.now().After(entry.expiresAt) {
		// Expirée : on la traite comme un miss (sera re-résolue et réécrite).
		return "", false
	}
	return entry.class, true
}

func (c *VeridianMXClassifier) cacheSet(domain, class string) {
	c.mu.Lock()
	c.cache[domain] = veridianMXCacheEntry{class: class, expiresAt: c.now().Add(c.ttl)}
	c.mu.Unlock()
}

// veridianNetMXResolver est l'implémentation de prod : un net.Resolver qui
// force un resolver UDP fiable (8.8.8.8 puis 1.1.1.1) plutôt que le resolver
// local du conteneur (vu instable en test : « communications error » sur
// 127.0.0.1#53, cf. ticket). Round-robin simple sur deux upstreams pour la
// résilience.
type veridianNetMXResolver struct {
	resolver *net.Resolver
}

func newVeridianNetMXResolver() *veridianNetMXResolver {
	upstreams := []string{"8.8.8.8:53", "1.1.1.1:53"}
	var counter uint64
	dialer := &net.Dialer{Timeout: veridianMXLookupTimeout}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			// Ignore l'adresse fournie par le runtime, force nos upstreams
			// fiables. Round-robin atomique léger pour répartir entre les deux
			// (et contourner un upstream momentanément KO au lookup suivant).
			i := int(atomic.AddUint64(&counter, 1)-1) % len(upstreams)
			return dialer.DialContext(ctx, network, upstreams[i])
		},
	}
	return &veridianNetMXResolver{resolver: r}
}

// LookupMXHosts résout les MX du domaine, triés par préférence (priorité MX
// croissante), et retourne les hostnames nettoyés du point terminal.
func (r *veridianNetMXResolver) LookupMXHosts(ctx context.Context, domain string) ([]string, error) {
	mxs, err := r.resolver.LookupMX(ctx, domain)
	if err != nil {
		return nil, err
	}
	// net.Resolver.LookupMX trie déjà par préférence ; on le garantit.
	sort.SliceStable(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
	hosts := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		h := strings.TrimSuffix(strings.ToLower(mx.Host), ".")
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts, nil
}

// LookupHostAddrs résout les A/AAAA du domaine (fallback « implicit MX » de la
// RFC 5321 §5.1 : un domaine sans MX mais avec un A reste délivrable en SMTP).
// Sert UNIQUEMENT à NE PAS classer mort un domaine qui n'a pas de MX mais qui
// reçoit quand même du mail sur son A.
func (r *veridianNetMXResolver) LookupHostAddrs(ctx context.Context, domain string) ([]string, error) {
	return r.resolver.LookupHost(ctx, domain)
}

// VeridianMXDeliverability classe le verdict de délivrabilité d'un domaine au
// niveau DNS, à l'usage du pré-filtrage d'envoi cold (Lot 7). Trois états, et
// SEUL `VeridianMXUndeliverable` justifie de bloquer un envoi (best-effort
// strict : l'indéterminé laisse TOUJOURS passer).
type VeridianMXDeliverability int

const (
	// VeridianMXIndeterminate : on ne peut pas conclure (timeout, erreur réseau
	// transitoire, resolver sans capacité host). NE JAMAIS bloquer là-dessus.
	VeridianMXIndeterminate VeridianMXDeliverability = iota
	// VeridianMXDeliverable : MX trouvé, OU suffixe public connu (gmail/orange…),
	// OU pas de MX mais un A/AAAA (implicit MX RFC 5321). L'envoi peut partir.
	VeridianMXDeliverable
	// VeridianMXUndeliverable : le DNS dit de façon DÉCISIVE que ce domaine ne
	// reçoit pas de mail (NXDOMAIN, ou aucun MX ET aucun A/AAAA). Verdict durable.
	VeridianMXUndeliverable
)

// veridianHostResolver est une capacité OPTIONNELLE d'un MXResolver : résoudre
// les A/AAAA d'un domaine. Détectée par type-assertion pour ne pas alourdir
// l'interface MXResolver (les resolvers de test n'ont pas à l'implémenter ;
// sans elle, le verdict « pas de MX » reste INDÉTERMINÉ — on ne bloque pas).
type veridianHostResolver interface {
	LookupHostAddrs(ctx context.Context, domain string) ([]string, error)
}

// veridianMXNotFound reconnaît un NXDOMAIN / « pas de tel host » DÉCISIF dans
// une erreur de resolver. Un timeout ou une erreur réseau retourne false (=
// indéterminé) : on ne bloquera jamais un envoi sur une glitch DNS transitoire.
func veridianMXNotFound(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// IsNotFound = NXDOMAIN ou pas d'enregistrement du type demandé : décisif.
		// IsTimeout / IsTemporary = transitoire : surtout PAS décisif.
		return dnsErr.IsNotFound && !dnsErr.IsTimeout && !dnsErr.IsTemporary
	}
	return false
}

// ResolveDeliverability établit le verdict DNS de délivrabilité d'un domaine
// pour le pré-filtrage cold (Lot 7). Best-effort STRICT, jamais d'erreur :
//  1. domaine vide → indéterminé (la syntaxe est vérifiée en amont) ;
//  2. suffixe public connu (gmail/orange/…) → délivrable SANS lookup (hot path) ;
//  3. lookup MX : au moins un MX → délivrable ;
//  4. pas de MX :
//     - erreur NON décisive (timeout/temp) → indéterminé (on laisse passer) ;
//     - NXDOMAIN décisif OU « no MX » : on tente le fallback A/AAAA (implicit MX) ;
//     A/AAAA présent → délivrable ; aucun A/AAAA (avec verdict décisif) →
//     UNDELIVERABLE ; sinon (pas de capacité host / verdict non décisif) →
//     indéterminé.
//
// Le cache MX du classifier (domaine→classe) n'est PAS réutilisé ici : il stocke
// une classe, pas un verdict de délivrabilité. Le lookup reste borné par
// veridianMXLookupTimeout et n'est tenté que pour les domaines à suffixe inconnu
// (l'immense majorité du cold custom passe d'abord par le tag amont / le throttle
// qui réchauffe de toute façon le DNS du domaine).
func (c *VeridianMXClassifier) ResolveDeliverability(ctx context.Context, domain string) VeridianMXDeliverability {
	if domain == "" {
		return VeridianMXIndeterminate
	}
	// Suffixe public connu = forcément délivrable, zéro I/O.
	if _, ok := classifyBySuffix(domain); ok {
		return VeridianMXDeliverable
	}

	lookupCtx, cancel := context.WithTimeout(ctx, veridianMXLookupTimeout)
	defer cancel()

	hosts, err := c.resolver.LookupMXHosts(lookupCtx, domain)
	if err == nil && len(hosts) > 0 {
		return VeridianMXDeliverable
	}

	// À partir d'ici : pas de MX exploitable. Décisif uniquement si NXDOMAIN /
	// « no such host », sinon transitoire → indéterminé.
	decisive := err == nil || veridianMXNotFound(err)
	if !decisive {
		return VeridianMXIndeterminate
	}

	// Fallback implicit MX (RFC 5321) : un A/AAAA suffit à recevoir du mail.
	hostRes, ok := c.resolver.(veridianHostResolver)
	if !ok {
		// Resolver sans capacité host → on ne tranche pas (best-effort).
		return VeridianMXIndeterminate
	}
	addrs, aErr := hostRes.LookupHostAddrs(lookupCtx, domain)
	if aErr == nil && len(addrs) > 0 {
		return VeridianMXDeliverable
	}
	if aErr == nil || veridianMXNotFound(aErr) {
		// Ni MX ni A/AAAA, de façon décisive → adresse morte.
		return VeridianMXUndeliverable
	}
	return VeridianMXIndeterminate
}
