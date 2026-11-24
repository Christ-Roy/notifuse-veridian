package reservoir

import "strings"

// Plateformes, mesurées dans ODH le 25/11/2026 (entreprises par domaine enregistrable) : site-solocal.com 29 848, business.site 27 506,
// eatbu.com 14 387, wordpress.com 10 141… Deux hôtels en *.site-solocal.com ne sont pas un doublon.

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// hostPlatforms : un sous-domaine par client. La clé est le sous-domaine, jamais la plateforme.
var hostPlatforms = set("site-solocal.com", "business.site", "free.fr", "eatbu.com", "wordpress.com", "e-monsite.com", "wix.com", "wixsite.com", "placestars.com",
	"jimdo.com", "jimdofree.com", "jimdosite.com", "pharminfo.fr", "placeweb.site", "over-blog.com", "mesoigner.fr", "pharmavie.fr", "pagesperso-orange.fr",
	"monsite-orange.fr", "webnode.fr", "webnode.com", "aprium-pharmacie.fr", "canalblog.com", "weebly.com", "footeo.com", "giropharm.fr", "sumupstore.com",
	"sitew.fr", "sitew.com", "zenchef.com", "webself.net", "sportsregions.fr", "pharm-upp.fr", "lafourchette.rest", "autosecurite.com", "clubeo.com",
	"ollca.com", "monopticien.com", "wifeo.com", "centres-sociaux.fr", "assoconnect.com", "canva.site", "securitest.fr", "catholique.fr", "epharmacie.pro",
	"unblog.fr", "asso-web.com", "pharmacorp.fr", "mytreatwell.fr", "metro.rest", "kalendes.com", "wellpharma.com", "blogspot.com", "squarespace.com",
	"strikingly.com", "myshopify.com", "godaddysites.com", "webador.fr", "site123.me", "carrd.co")

// pathPlatforms : une page par client sous la même adresse (réseaux, annuaires, liens courts). Jamais une clé.
var pathPlatforms = set("facebook.com", "instagram.com", "linkedin.com", "twitter.com", "x.com", "youtube.com", "tiktok.com", "planity.com", "doctolib.fr",
	"helloasso.com", "linktr.ee", "bit.ly", "g.co", "goo.gl", "google.com", "google.fr", "pagesjaunes.fr", "tripadvisor.fr", "tripadvisor.com", "airbnb.fr",
	"airbnb.com", "booking.com", "leboncoin.fr", "mappy.com", "service-public.fr", "laposte.fr", "lefigaro.fr", "wikipedia.org", "eater.space", "points.fr",
	"parkopedia.fr", "lafourchette.com", "thefork.fr", "ubereats.com")

// platformKey : sous une plateforme à sous-domaines, la clé est le nom du client (www compris dans la coupe).
func platformKey(host, registrable string) string {
	if !hostPlatforms[registrable] || host == registrable {
		return registrable
	}
	labels := strings.Split(strings.TrimSuffix(host, "."+registrable), ".")
	own := labels[len(labels)-1]
	if own == "www" {
		return registrable
	}
	return own + "." + registrable
}
