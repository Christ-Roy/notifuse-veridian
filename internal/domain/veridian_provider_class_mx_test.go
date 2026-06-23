package domain

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMXResolver est un MXResolver de test : map domaine→hosts, + compteur de
// lookups pour vérifier le cache, + option d'erreur/latence pour le best-effort.
type fakeMXResolver struct {
	mu       sync.Mutex
	byDomain map[string][]string
	err      error         // si non nil, renvoyé pour tout domaine
	delay    time.Duration // simule un lookup lent (test timeout)
	calls    int32         // nombre total de LookupMXHosts appelés
}

func (f *fakeMXResolver) LookupMXHosts(ctx context.Context, domain string) ([]string, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	hosts, ok := f.byDomain[domain]
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("no MX (NXDOMAIN)")
	}
	return hosts, nil
}

func (f *fakeMXResolver) callCount() int32 { return atomic.LoadInt32(&f.calls) }

// TestClassifyMXHost couvre la table de patterns MX→classe TELLE QU'ELLE est
// dérivée de la vraie data (un cas par famille du ticket), case-insensitive,
// suffixe, et l'ordre de priorité (gateway avant hébergeur).
func TestClassifyMXHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		// Google
		{"aspmx google", "aspmx.l.google.com", ProviderClassGoogle},
		{"alt aspmx google", "alt1.aspmx.l.google.com", ProviderClassGoogle},
		{"gmail-smtp-in google", "gmail-smtp-in.l.google.com", ProviderClassGoogle},
		{"google UPPERCASE", "ASPMX.L.GOOGLE.COM", ProviderClassGoogle},
		{"google mixed case", "AspMx.L.Google.Com", ProviderClassGoogle},
		{"google trailing dot FQDN", "aspmx.l.google.com.", ProviderClassGoogle},
		{"googlemail variant", "gmail-smtp-in.l.googlemail.com", ProviderClassGoogle},

		// Microsoft
		{"outlook protection", "veridian-site.mail.protection.outlook.com", ProviderClassMicrosoft},
		{"olc protection outlook", "host.olc.protection.outlook.com", ProviderClassMicrosoft},
		{"outlook plain", "mx.outlook.com", ProviderClassMicrosoft},

		// OVH
		{"ovh mx1", "mx1.mail.ovh.net", ProviderClassOVH},
		{"ovh mx0", "mx0.mail.ovh.net", ProviderClassOVH},

		// IONOS
		{"ionos fr", "mx00.ionos.fr", ProviderClassIonos},
		{"ionos de", "mx01.ionos.de", ProviderClassIonos},
		{"kundenserver", "mx00.kundenserver.de", ProviderClassIonos},

		// Yahoo / AOL
		{"yahoodns", "mta5.am0.yahoodns.net", ProviderClassYahooAol},

		// Freemail FR (host MX)
		{"orange smtp-in", "smtp-in.orange.fr", ProviderClassFreemailFR},
		{"free mx", "mx1.free.fr", ProviderClassFreemailFR},

		// Apple iCloud
		{"icloud mail", "mx01.mail.icloud.com", ProviderClassAppleICloud},

		// Security gateways (PRIORITÉ : testées avant les hébergeurs)
		{"vadesecure", "mx.vadesecure.com", ProviderClassSecurityGateway},
		{"mailinblack", "smtp.mailinblack.com", ProviderClassSecurityGateway},
		{"proofpoint pphosted", "mx1.eu1.pphosted.com", ProviderClassSecurityGateway},
		{"mimecast", "eu-smtp-inbound-1.mimecast.com", ProviderClassSecurityGateway},
		{"hornetsecurity", "mx.hornetsecurity.com", ProviderClassSecurityGateway},
		{"barracuda cudasvc", "mx.region.cudasvc.com", ProviderClassSecurityGateway},
		{"messagelabs", "cluster.eu.messagelabs.com", ProviderClassSecurityGateway},

		// Other hosters
		{"infomaniak", "mta-gw.infomaniak.ch", ProviderClassOtherHoster},
		{"gandi", "spool.mail.gandi.net", ProviderClassOtherHoster},
		{"hostinger", "mx1.hostinger.com", ProviderClassOtherHoster},
		{"zoho", "mx.zoho.eu", ProviderClassOtherHoster},
		{"proton", "mail.protonmail.ch", ProviderClassOtherHoster},
		{"online scaleway", "mx.online.net", ProviderClassOtherHoster},

		// Unknown → no match (caller falls back to corporate_selfhost)
		{"unknown self-host", "mail.cabinet-dupont.fr", ""},
		{"empty host", "", ""},
		{"random", "smtp.some-random-host.tld", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := classifyMXHost(tt.host)
			if tt.want == "" {
				assert.False(t, ok, "expected no match for %q", tt.host)
				return
			}
			require.True(t, ok, "expected a match for %q", tt.host)
			assert.Equal(t, tt.want, got)
			// Toute classe retournée par la table DOIT être canonique (sinon les
			// consommateurs throttle/cap/pixel la rejetteraient).
			assert.True(t, IsValidProviderClass(got), "classe %q non canonique", got)
		})
	}
}

// TestClassifyMXHosts vérifie qu'on prend le PREMIER host reconnu d'une liste.
func TestClassifyMXHosts(t *testing.T) {
	t.Run("first recognized wins", func(t *testing.T) {
		class, ok := classifyMXHosts([]string{"unknown.tld", "aspmx.l.google.com"})
		require.True(t, ok)
		assert.Equal(t, ProviderClassGoogle, class)
	})
	t.Run("none recognized", func(t *testing.T) {
		_, ok := classifyMXHosts([]string{"a.tld", "b.tld"})
		assert.False(t, ok)
	})
	t.Run("empty list", func(t *testing.T) {
		_, ok := classifyMXHosts(nil)
		assert.False(t, ok)
	})
}

// TestMXClassifier_SuffixKnownNoLookup : un domaine grand public connu par
// suffixe ne déclenche AUCUN lookup MX (hot path, non-régression).
func TestMXClassifier_SuffixKnownNoLookup(t *testing.T) {
	res := &fakeMXResolver{byDomain: map[string][]string{}}
	c := NewVeridianMXClassifier(res)

	for _, tc := range []struct {
		email string
		want  string
	}{
		{"jean@gmail.com", ProviderClassGoogle},
		{"a@outlook.fr", ProviderClassMicrosoft},
		{"a@orange.fr", ProviderClassFreemailFR},
		{"a@yahoo.com", ProviderClassYahooAol},
	} {
		assert.Equal(t, tc.want, c.ClassifyEmail(context.Background(), tc.email))
	}
	assert.Equal(t, int32(0), res.callCount(), "aucun lookup MX ne doit partir pour un suffixe connu")
}

// TestMXClassifier_CustomDomainResolvedByMX : LE cœur du Lot 4. Un domaine
// custom inconnu par suffixe est classé selon son MX réel.
func TestMXClassifier_CustomDomainResolvedByMX(t *testing.T) {
	res := &fakeMXResolver{byDomain: map[string][]string{
		"cabinet-dupont.fr": {"veridian.mail.protection.outlook.com"}, // M365
		"udevweb.co":        {"aspmx.l.google.com"},                   // Google Workspace
		"agence-immo.fr":    {"mx1.mail.ovh.net"},                     // OVH
		"startup.io":        {"mx00.ionos.fr"},                        // IONOS
		"protected-corp.fr": {"mx.vadesecure.com"},                    // gateway
		"vraie-pme.fr":      {"mail.vraie-pme.fr"},                    // self-host inconnu
	}}
	c := NewVeridianMXClassifier(res)

	cases := []struct {
		email string
		want  string
	}{
		{"contact@cabinet-dupont.fr", ProviderClassMicrosoft},
		{"hello@udevweb.co", ProviderClassGoogle},
		{"info@agence-immo.fr", ProviderClassOVH},
		{"ceo@startup.io", ProviderClassIonos},
		{"rh@protected-corp.fr", ProviderClassSecurityGateway},
		{"gerant@vraie-pme.fr", ProviderClassCorporateSelfhost}, // MX connu mais pattern inconnu
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, c.ClassifyEmail(context.Background(), tc.email), tc.email)
	}
}

// TestMXClassifier_CacheHit : un domaine custom n'est résolu qu'UNE fois, les
// envois suivants viennent du cache (zéro lookup supplémentaire).
func TestMXClassifier_CacheHit(t *testing.T) {
	res := &fakeMXResolver{byDomain: map[string][]string{
		"cabinet-dupont.fr": {"veridian.mail.protection.outlook.com"},
	}}
	c := NewVeridianMXClassifier(res)

	for i := 0; i < 5; i++ {
		assert.Equal(t, ProviderClassMicrosoft, c.ClassifyEmail(context.Background(), "x@cabinet-dupont.fr"))
	}
	assert.Equal(t, int32(1), res.callCount(), "le domaine ne doit être résolu qu'une fois (cache)")
}

// TestMXClassifier_CacheMissAfterTTL : passé le TTL, l'entrée est re-résolue.
func TestMXClassifier_CacheMissAfterTTL(t *testing.T) {
	res := &fakeMXResolver{byDomain: map[string][]string{
		"cabinet-dupont.fr": {"aspmx.l.google.com"},
	}}
	c := NewVeridianMXClassifier(res)

	// Horloge contrôlée pour franchir le TTL sans attendre.
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	c.now = func() time.Time { return time.Unix(0, clock.Load()) }

	assert.Equal(t, ProviderClassGoogle, c.ClassifyDomain(context.Background(), "cabinet-dupont.fr"))
	assert.Equal(t, int32(1), res.callCount())

	// Avance au-delà du TTL → re-lookup.
	clock.Add(int64(veridianMXCacheTTL) + int64(time.Hour))
	assert.Equal(t, ProviderClassGoogle, c.ClassifyDomain(context.Background(), "cabinet-dupont.fr"))
	assert.Equal(t, int32(2), res.callCount(), "après TTL le domaine doit être re-résolu")
}

// TestMXClassifier_LookupErrorFallback : NXDOMAIN / erreur DNS → corporate_selfhost,
// et le résultat est caché (pas de re-lookup en boucle sur un domaine mort).
func TestMXClassifier_LookupErrorFallback(t *testing.T) {
	res := &fakeMXResolver{err: errors.New("communications error")}
	c := NewVeridianMXClassifier(res)

	assert.Equal(t, ProviderClassCorporateSelfhost, c.ClassifyEmail(context.Background(), "x@dead-domain.tld"))
	assert.Equal(t, ProviderClassCorporateSelfhost, c.ClassifyEmail(context.Background(), "x@dead-domain.tld"))
	assert.Equal(t, int32(1), res.callCount(), "un échec est caché : pas de re-lookup à chaque envoi")
}

// TestMXClassifier_TimeoutFallback : un lookup plus lent que le timeout dégrade
// vers corporate_selfhost SANS bloquer (best-effort strict).
func TestMXClassifier_TimeoutFallback(t *testing.T) {
	res := &fakeMXResolver{
		byDomain: map[string][]string{"slow.fr": {"aspmx.l.google.com"}},
		delay:    veridianMXLookupTimeout + 500*time.Millisecond,
	}
	c := NewVeridianMXClassifier(res)

	start := time.Now()
	got := c.ClassifyEmail(context.Background(), "x@slow.fr")
	elapsed := time.Since(start)

	assert.Equal(t, ProviderClassCorporateSelfhost, got)
	assert.Less(t, elapsed, veridianMXLookupTimeout+400*time.Millisecond,
		"le timeout doit couper bien avant que le lookup lent ne réponde")
}

// TestMXClassifier_InvalidEmail : adresse vide / sans @ → corporate_selfhost,
// jamais de panic, jamais de lookup.
func TestMXClassifier_InvalidEmail(t *testing.T) {
	res := &fakeMXResolver{byDomain: map[string][]string{}}
	c := NewVeridianMXClassifier(res)
	for _, bad := range []string{"", "not-an-email", "jean@", "@", "a@ "} {
		assert.Equal(t, ProviderClassCorporateSelfhost, c.ClassifyEmail(context.Background(), bad), bad)
	}
	assert.Equal(t, int32(0), res.callCount())
}

// TestMXClassifier_NilResolverUsesNetDefault : un classifier construit sans
// resolver injecté ne panique pas (resolver réseau par défaut installé).
func TestMXClassifier_NilResolverUsesNetDefault(t *testing.T) {
	c := NewVeridianMXClassifier(nil)
	require.NotNil(t, c)
	// Suffixe connu : réponse directe sans toucher au réseau.
	assert.Equal(t, ProviderClassGoogle, c.ClassifyEmail(context.Background(), "x@gmail.com"))
}

// TestVeridianAllProviderClasses_Canonical : toutes les classes énumérées sont
// canoniques et le set contient EXACTEMENT ces classes (garde-fou contre un
// oubli d'ajout dans le set ou la liste).
func TestVeridianAllProviderClasses_Canonical(t *testing.T) {
	all := VeridianAllProviderClasses()
	require.Len(t, all, 11, "11 classes attendues (5 historiques + 6 MX)")
	seen := map[string]bool{}
	for _, c := range all {
		assert.True(t, IsValidProviderClass(c), "classe %q non valide", c)
		assert.False(t, seen[c], "doublon %q", c)
		seen[c] = true
	}
	// Les 5 historiques DOIVENT toujours être présentes (non-régression).
	for _, h := range []string{
		ProviderClassGoogle, ProviderClassMicrosoft, ProviderClassYahooAol,
		ProviderClassFreemailFR, ProviderClassCorporate,
	} {
		assert.Contains(t, all, h)
	}
}

// TestMXClassifier_ConcurrentSafe : le cache est thread-safe (race detector).
func TestMXClassifier_ConcurrentSafe(t *testing.T) {
	res := &fakeMXResolver{byDomain: map[string][]string{
		"a.fr": {"aspmx.l.google.com"},
		"b.fr": {"mx1.mail.ovh.net"},
	}}
	c := NewVeridianMXClassifier(res)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				assert.Equal(t, ProviderClassGoogle, c.ClassifyEmail(context.Background(), "x@a.fr"))
			} else {
				assert.Equal(t, ProviderClassOVH, c.ClassifyEmail(context.Background(), "x@b.fr"))
			}
		}(i)
	}
	wg.Wait()
}

// TestMXClassifier_CacheCapEvictsExpired : au cap, cacheSet purge d'abord les
// entrées EXPIRÉES (gratuit en pratique vu le TTL de 7j) → le cache ne fuit pas.
func TestMXClassifier_CacheCapEvictsExpired(t *testing.T) {
	c := NewVeridianMXClassifier(&fakeMXResolver{})

	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	c.now = func() time.Time { return time.Unix(0, clock.Load()) }

	// Remplis le cache jusqu'au cap avec des entrées qui vont expirer.
	c.mu.Lock()
	for i := 0; i < veridianMXCacheMaxEntries; i++ {
		c.cache["expired"+strconv.Itoa(i)+".tld"] = veridianMXCacheEntry{
			class: ProviderClassCorporateSelfhost, expiresAt: c.now().Add(c.ttl),
		}
	}
	c.mu.Unlock()

	// Avance au-delà du TTL : toutes les entrées sont expirées.
	clock.Add(int64(veridianMXCacheTTL) + int64(time.Hour))

	// Un nouvel insert au cap doit purger les expirées et NE PAS dépasser le cap.
	c.cacheSet("fresh.tld", ProviderClassGoogle)

	c.mu.RLock()
	size := len(c.cache)
	_, freshPresent := c.cache["fresh.tld"]
	c.mu.RUnlock()

	assert.Equal(t, 1, size, "les entrées expirées doivent être purgées, ne laissant que la fraîche")
	assert.True(t, freshPresent, "la nouvelle entrée doit être présente après la purge")
}

// TestMXClassifier_CacheCapHardResetWhenAllFresh : cas extrême (> cap domaines
// distincts tous DANS le TTL) → vidage de secours, le cache reste borné.
func TestMXClassifier_CacheCapHardResetWhenAllFresh(t *testing.T) {
	c := NewVeridianMXClassifier(&fakeMXResolver{})

	// Remplis au cap avec des entrées NON expirées (toutes fraîches).
	c.mu.Lock()
	for i := 0; i < veridianMXCacheMaxEntries; i++ {
		c.cache["fresh"+strconv.Itoa(i)+".tld"] = veridianMXCacheEntry{
			class: ProviderClassCorporateSelfhost, expiresAt: c.now().Add(c.ttl),
		}
	}
	c.mu.Unlock()

	c.cacheSet("brandnew.tld", ProviderClassMicrosoft)

	c.mu.RLock()
	size := len(c.cache)
	_, present := c.cache["brandnew.tld"]
	c.mu.RUnlock()

	assert.LessOrEqual(t, size, veridianMXCacheMaxEntries, "le cache ne doit JAMAIS dépasser le cap")
	assert.True(t, present, "la nouvelle entrée doit survivre au vidage de secours")
}

// --- Lot 7 : ResolveDeliverability (pré-filtrage DNS best-effort) -----------

// fakeDeliverabilityResolver est un MXResolver de test qui implémente AUSSI la
// capacité host optionnelle, et qui peut simuler chaque cas DNS : MX présent,
// NXDOMAIN décisif, timeout transitoire, et présence/absence d'A record.
type fakeDeliverabilityResolver struct {
	mxByDomain   map[string][]string
	mxErr        map[string]error // erreur de LookupMXHosts par domaine
	addrByDomain map[string][]string
	addrErr      map[string]error // erreur de LookupHostAddrs par domaine
}

func (f *fakeDeliverabilityResolver) LookupMXHosts(_ context.Context, domain string) ([]string, error) {
	if err, ok := f.mxErr[domain]; ok {
		return nil, err
	}
	if hosts, ok := f.mxByDomain[domain]; ok {
		return hosts, nil
	}
	// défaut : pas de MX, sans erreur explicite → liste vide sans erreur
	return nil, nil
}

func (f *fakeDeliverabilityResolver) LookupHostAddrs(_ context.Context, domain string) ([]string, error) {
	if err, ok := f.addrErr[domain]; ok {
		return nil, err
	}
	if addrs, ok := f.addrByDomain[domain]; ok {
		return addrs, nil
	}
	return nil, nil
}

// resolverWithoutHostCap implémente UNIQUEMENT MXResolver (pas la capacité host),
// pour vérifier que sans LookupHostAddrs le verdict « pas de MX » reste indéterminé.
type resolverWithoutHostCap struct{ err error }

func (r resolverWithoutHostCap) LookupMXHosts(_ context.Context, _ string) ([]string, error) {
	return nil, r.err
}

func nxdomainErr() error {
	return &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}
}

func timeoutErr() error {
	return &net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}
}

func TestResolveDeliverability(t *testing.T) {
	res := &fakeDeliverabilityResolver{
		mxByDomain: map[string][]string{
			"has-mx.example": {"mx1.has-mx.example"},
		},
		mxErr: map[string]error{
			"nxdomain.invalid":      nxdomainErr(), // MX NXDOMAIN décisif
			"timeout.example":       timeoutErr(),  // MX timeout transitoire
			"nomx-hasa.example":     nxdomainErr(), // pas de MX (décisif) mais A présent
			"nomx-noa.example":      nxdomainErr(), // ni MX ni A (décisif)
			"nomx-atimeout.example": nxdomainErr(), // MX décisif mais A timeout → indéterminé
		},
		addrByDomain: map[string][]string{
			"nomx-hasa.example": {"203.0.113.7"}, // implicit MX RFC 5321
		},
		addrErr: map[string]error{
			"nxdomain.invalid":      nxdomainErr(), // pas d'A non plus → mort
			"nomx-noa.example":      nxdomainErr(), // pas d'A → mort
			"nomx-atimeout.example": timeoutErr(),  // A transitoire → indéterminé
		},
	}
	c := NewVeridianMXClassifier(res)

	tests := []struct {
		name   string
		domain string
		want   VeridianMXDeliverability
	}{
		{"suffixe public connu = délivrable sans lookup", "gmail.com", VeridianMXDeliverable},
		{"MX présent = délivrable", "has-mx.example", VeridianMXDeliverable},
		{"NXDOMAIN + pas d'A = mort", "nxdomain.invalid", VeridianMXUndeliverable},
		{"pas de MX mais A présent = délivrable (implicit MX)", "nomx-hasa.example", VeridianMXDeliverable},
		{"ni MX ni A (décisif) = mort", "nomx-noa.example", VeridianMXUndeliverable},
		{"MX timeout transitoire = indéterminé (laisse passer)", "timeout.example", VeridianMXIndeterminate},
		{"MX décisif mais A timeout = indéterminé (laisse passer)", "nomx-atimeout.example", VeridianMXIndeterminate},
		{"domaine vide = indéterminé", "", VeridianMXIndeterminate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.ResolveDeliverability(context.Background(), tt.domain)
			assert.Equal(t, tt.want, got, "domaine %q", tt.domain)
		})
	}
}

func TestResolveDeliverability_NoHostCapStaysIndeterminate(t *testing.T) {
	// Resolver sans capacité host : un « pas de MX » même décisif ne peut pas être
	// confirmé mort (pas de fallback A) → on ne bloque JAMAIS (best-effort).
	c := NewVeridianMXClassifier(resolverWithoutHostCap{err: nxdomainErr()})
	got := c.ResolveDeliverability(context.Background(), "unknown-custom.example")
	assert.Equal(t, VeridianMXIndeterminate, got)
}

func TestVeridianMXNotFound(t *testing.T) {
	assert.False(t, veridianMXNotFound(nil), "nil = pas décisif")
	assert.True(t, veridianMXNotFound(nxdomainErr()), "NXDOMAIN = décisif")
	assert.False(t, veridianMXNotFound(timeoutErr()), "timeout = pas décisif")
	assert.False(t, veridianMXNotFound(errors.New("plain error")), "erreur générique = pas décisif")
	// IsNotFound + IsTemporary simultané → on reste prudent (pas décisif).
	tempNotFound := &net.DNSError{Err: "x", IsNotFound: true, IsTemporary: true}
	assert.False(t, veridianMXNotFound(tempNotFound), "not-found temporaire = pas décisif")
}
