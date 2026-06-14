package domain

// Veridian — pixel d'ouverture (email.opened) PAR CLASSE de provider destinataire.
//
// Le pixel d'ouverture upstream est gouverné par le même flag que la réécriture
// de liens (TrackingSettings.EnableTracking). Pour le cold mailing, on veut
// l'ouverture trackée sur les petits providers peu sensibles au pixel
// (freemail_fr, yahoo_aol, corporate) mais PAS sur google/microsoft (réputation),
// tout en gardant le tracking de CLICS partout. Ce fichier porte la politique
// par classe ; le découplage pixel/liens vit dans
// pkg/notifuse_mjml/template_compilation.go (champ TrackingSettings.EnableOpenPixel).
//
// Contrat (figé lead 2026-06-10, ticket todo/2026-06-10-open-tracking-petits-providers.md,
// DoD V1 §1.3) :
//   - défaut tunnel : pixel ON pour freemail_fr/yahoo_aol/corporate, OFF pour
//     google/microsoft.
//   - configurable : broadcast.metadata["veridian_open_pixel_by_class"] (map
//     classe→bool) puis workspace settings veridian_open_pixel_by_class (fallback),
//     même esprit que veridian_provider_class_rates.
//   - NON-RÉGRESSION : hors contexte tunnel (aucun tag classe, aucune config
//     pixel/rates), le resolver retourne nil → le pixel suit EnableTracking
//     comme upstream. Un broadcast classique ne change PAS de comportement.

// VeridianOpenPixelByClassMetadataKey est la clé de broadcast.Metadata portant
// la map {classe: bool} qui override la politique pixel par défaut.
const VeridianOpenPixelByClassMetadataKey = "veridian_open_pixel_by_class"

// veridianDefaultOpenPixelByClass = politique par défaut quand le tunnel est
// actif : ON sur les petits providers, OFF sur les gros / sensibles (réputation).
// Révisable data-driven par config sans toucher au code.
//
// Classes MX (Lot 4) : pixel OFF sur les gros / sensibles
// (google/microsoft/security_gateway — les gateways anti-spam scrutent les pixels),
// ON sur les nébuleuses FR et hébergeurs propres (ovh/ionos/apple_icloud/
// other_hoster/corporate_selfhost) moins regardants. Toute classe absente de
// cette map prend le zéro-value `false` (pixel OFF), donc un défaut prudent.
var veridianDefaultOpenPixelByClass = map[string]bool{
	// Historiques.
	ProviderClassGoogle:     false,
	ProviderClassMicrosoft:  false,
	ProviderClassYahooAol:   true,
	ProviderClassFreemailFR: true,
	ProviderClassCorporate:  true,
	// MX (Lot 4).
	ProviderClassOVH:               true,
	ProviderClassIonos:             true,
	ProviderClassAppleICloud:       false, // Apple = règles strictes → prudent
	ProviderClassSecurityGateway:   false, // anti-spam pro → surtout pas de pixel
	ProviderClassOtherHoster:       true,
	ProviderClassCorporateSelfhost: true,
}

// VeridianOpenPixelByClassFromMetadata extrait la map {classe: bool} d'un
// broadcast.Metadata. Seules les classes canoniques sont retenues ; une config
// malformée est ignorée silencieusement (jamais bloquant). Retourne nil si la
// clé est absente ou ne contient aucune entrée exploitable.
func VeridianOpenPixelByClassFromMetadata(metadata MapOfAny) map[string]bool {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[VeridianOpenPixelByClassMetadataKey]
	if !ok {
		return nil
	}

	out := make(map[string]bool)
	switch m := raw.(type) {
	case map[string]any:
		for class, v := range m {
			if !IsValidProviderClass(class) {
				continue
			}
			if b, ok := veridianToBool(v); ok {
				out[class] = b
			}
		}
	case map[string]bool:
		for class, b := range m {
			if IsValidProviderClass(class) {
				out[class] = b
			}
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// veridianToBool normalise un bool après round-trip JSON (toujours bool, mais
// on tolère aussi les représentations numériques 0/1 par robustesse).
func veridianToBool(v any) (bool, bool) {
	switch n := v.(type) {
	case bool:
		return n, true
	case float64:
		return n != 0, true
	case int:
		return n != 0, true
	}
	return false, false
}

// VeridianResolveOpenPixel calcule le flag pixel d'ouverture EFFECTIF pour un
// destinataire, par classe de provider. Retourne :
//   - nil  → hors contexte tunnel : laisser le comportement upstream (le pixel
//     suit trackingEnabled). NON-RÉGRESSION stricte.
//   - &b   → contexte tunnel détecté : pixel forcé ON (true) ou OFF (false)
//     selon la classe, indépendamment de trackingEnabled.
//
// "Contexte tunnel" = au moins un signal tunnel présent : tag custom_string_5
// sur le contact, OU config pixel (broadcast/workspace), OU config rates de
// throttle par classe (broadcast/workspace). Sans aucun de ces signaux, un
// broadcast est traité comme un broadcast classique upstream → nil.
//
// Précédence de la politique par classe : broadcast metadata > workspace
// settings > défaut tunnel (veridianDefaultOpenPixelByClass).
func VeridianResolveOpenPixel(contact *Contact, email string, broadcast *Broadcast, workspace *Workspace) *bool {
	var (
		broadcastPixel map[string]bool
		workspacePixel map[string]bool
		broadcastRates map[string]float64
		workspaceRates map[string]float64
	)
	if broadcast != nil {
		broadcastPixel = VeridianOpenPixelByClassFromMetadata(broadcast.Metadata)
		broadcastRates = VeridianProviderClassRatesFromMetadata(broadcast.Metadata)
	}
	if workspace != nil {
		workspacePixel = workspace.Settings.VeridianOpenPixelByClass
		workspaceRates = workspace.Settings.VeridianProviderClassRates
	}

	// Détection du contexte tunnel : un signal suffit.
	contactClass := VeridianContactProviderClass(contact)
	tunnelActive := contactClass != "" ||
		len(broadcastPixel) > 0 || len(workspacePixel) > 0 ||
		len(broadcastRates) > 0 || len(workspaceRates) > 0
	if !tunnelActive {
		return nil
	}

	// Classe effective du destinataire : tag contact (option B) sinon
	// classification locale par suffixe (fallback robuste, jamais bloquant).
	class := contactClass
	if class == "" {
		class = ClassifyProviderClass(email)
	}

	// Politique par classe : broadcast > workspace > défaut tunnel.
	if broadcastPixel != nil {
		if b, ok := broadcastPixel[class]; ok {
			return &b
		}
	}
	if workspacePixel != nil {
		if b, ok := workspacePixel[class]; ok {
			return &b
		}
	}
	b := veridianDefaultOpenPixelByClass[class]
	return &b
}
