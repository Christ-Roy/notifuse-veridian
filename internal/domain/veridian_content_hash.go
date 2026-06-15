package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Veridian — ANTI-HASH IDENTIQUE par classe de provider destinataire (cold
// outbound). Empêche que deux mails au RENDU FINAL identique (sujet + corps,
// après Liquid + spintax) partent vers la MÊME classe de provider destinataire
// dans une fenêtre glissante. Deux mails byte-identiques (modulo whitespace)
// vers le même provider = empreinte de campagne triviale (fuzzy hashing type
// Nilsimsa/ssdeep, clustering de n-grams). Le spintax SEUL ne suffit pas :
// 1 groupe {A|B} = 2 variantes → collisions massives sur 500 envois.
//
// On bloque l'IDENTITÉ DE HASH (rendu identique), PAS la similarité floue
// (reproduire un moteur de fuzzy hashing serait l'usine à gaz refusée). C'est le
// signal le plus net et le moins discutable.
//
// Ce fichier est PUR : hash + parsing de config (depuis metadata broadcast /
// settings workspace). L'orchestration (détection collision + re-spin à
// l'enqueue, filet best-effort au worker) vit dans les services
// (veridian_content_dedup.go, veridian_content_hash_gate.go).

// VeridianAntiHashMetadataKeyEnabled active/désactive l'anti-hash via
// broadcast.metadata. Bool : true = activé, false = désactivé explicitement.
const VeridianAntiHashMetadataKeyEnabled = "veridian_anti_hash_enabled"

// VeridianAntiHashMetadataKeyWindowHours porte la fenêtre glissante (heures)
// via broadcast.metadata. <=0 / absent = défaut (VeridianDefaultAntiHashWindow).
const VeridianAntiHashMetadataKeyWindowHours = "veridian_anti_hash_window_hours"

// VeridianDefaultAntiHashWindow est la fenêtre glissante par défaut. Au-delà,
// deux mails identiques ne forment plus une empreinte de CAMPAGNE ; borne la
// taille de l'espace de recherche. 72h aligné sur l'ordre de grandeur du daily
// cap (ticket 2026-06-15).
const VeridianDefaultAntiHashWindow = 72 * time.Hour

// VeridianContentHash calcule un hash stable du rendu final normalisé d'un
// email (sujet + corps), pour détecter deux mails au contenu identique partant
// vers la même classe de provider destinataire. PUR, déterministe.
//
// Normalisation (avant hash) : lowercase + collapse de tous les blancs
// (espaces/tabs/newlines consécutifs → un seul espace) + trim. But : détecter
// l'identité de CAMPAGNE (deux mails identiques modulo mise en forme), pas
// l'identité stricte byte-à-byte. On ne normalise PAS au-delà (pas de strip de
// ponctuation / d'URL : ça masquerait une vraie variation). Le sujet ET le corps
// comptent (un sujet identique est aussi une empreinte). Séparateur \x00 entre
// sujet et corps pour éviter qu'un déplacement de frontière sujet/corps produise
// le même hash. SHA-256 tronqué à 128 bits (16 octets → 32 hex) : largement
// suffisant pour une détection d'égalité dans une fenêtre courte (collision
// accidentelle négligeable), et compact en DB (char(32)).
func VeridianContentHash(subject, body string) string {
	normalized := veridianNormalizeForHash(subject) + "\x00" + veridianNormalizeForHash(body)
	sum := sha256.Sum256([]byte(normalized))
	// Tronqué à 16 octets = 128 bits.
	return hex.EncodeToString(sum[:16])
}

// veridianNormalizeForHash normalise une chaîne pour le hash de contenu :
// lowercase, collapse des séquences de blancs en un seul espace, trim. strings.
// Fields gère tous les types de blancs Unicode (espace, tab, \n, \r, …) en une
// passe sans allocation excessive.
func veridianNormalizeForHash(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// VeridianAntiHashEnabledFor résout si l'anti-hash est actif via la cascade
// metadata broadcast → infra → workspace. enabledPtr (workspace/infra) : nil =
// non configuré, *true/*false = override explicite. metaEnabled/metaOK : valeur
// extraite du broadcast (ok=false si absent). coldDefault = défaut appliqué
// quand AUCUN niveau ne décide (true en contexte cold). Premier niveau DÉFINI
// gagne : broadcast (si présent) → infra → workspace → défaut.
func VeridianAntiHashEnabledFor(metaEnabled, metaOK bool, infra, workspace *bool, coldDefault bool) bool {
	if metaOK {
		return metaEnabled
	}
	if infra != nil {
		return *infra
	}
	if workspace != nil {
		return *workspace
	}
	return coldDefault
}

// VeridianAntiHashWindow résout la fenêtre glissante via la cascade metadata
// broadcast → infra → workspace → défaut. Chaque niveau exprime la fenêtre en
// HEURES (int) ; <=0 = niveau non configuré (on passe au suivant). Premier
// niveau strictement positif gagne. Aucun niveau → VeridianDefaultAntiHashWindow.
func VeridianAntiHashWindow(metaHours, infraHours, workspaceHours int) time.Duration {
	for _, h := range []int{metaHours, infraHours, workspaceHours} {
		if h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return VeridianDefaultAntiHashWindow
}

// VeridianAntiHashEnabledFromMetadata extrait (enabled, present) depuis un
// broadcast.Metadata. present=false si la clé est absente / non-booléenne.
func VeridianAntiHashEnabledFromMetadata(metadata MapOfAny) (enabled bool, present bool) {
	if metadata == nil {
		return false, false
	}
	raw, ok := metadata[VeridianAntiHashMetadataKeyEnabled]
	if !ok {
		return false, false
	}
	if b, ok := raw.(bool); ok {
		return b, true
	}
	return false, false
}

// VeridianAntiHashWindowHoursFromMetadata extrait la fenêtre (heures) depuis un
// broadcast.Metadata. Retourne 0 si absent / malformé / <=0 (= non configuré au
// niveau broadcast, le résolveur passe au niveau suivant).
func VeridianAntiHashWindowHoursFromMetadata(metadata MapOfAny) int {
	if metadata == nil {
		return 0
	}
	raw, ok := metadata[VeridianAntiHashMetadataKeyWindowHours]
	if !ok {
		return 0
	}
	if h, ok := veridianToInt(raw); ok && h > 0 {
		return h
	}
	return 0
}
