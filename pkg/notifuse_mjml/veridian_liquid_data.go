package notifuse_mjml

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Les données d'un contact, d'un flux ou de l'API sont du TEXTE, jamais du modèle.
//
// CompileTemplate passe le MJML par Liquid plusieurs fois (par bloc, puis sur la
// chaîne entière pour les blocs mj-liquid) et applique le spintax ensuite. Une
// valeur de donnée qui contient « {{ ... }} », « {% ... %} » ou « {a|b} » serait
// donc évaluée à la passe suivante : un nom d'entreprise scrapé pouvait lire une
// autre variable, ouvrir une boucle ou voir ses accolades résolues en spintax.
// Correctif de la classe de défauts v42 (« Liquid inside contact data »),
// réécrit pour le fork à partir de la description publique de l'advisory.
//
// Méthode : avant tout rendu, les accolades des VALEURS texte sont remplacées par
// deux caractères de la zone d'usage privé Unicode ; aucune passe ne les voit.
// Juste avant la compilation MJML, elles sont remises. Les clés et les types
// non texte ne changent pas ; l'entrée n'est jamais modifiée.
const (
	veridianBraceOpenSentinel  = ''
	veridianBraceCloseSentinel = ''
)

var veridianBraceReplacer = strings.NewReplacer(
	"{", string(veridianBraceOpenSentinel),
	"}", string(veridianBraceCloseSentinel),
)

// Un filtre Liquid url_encode transforme les sentinelles en octets UTF-8 encodés ;
// on les ramène à l'encodage des accolades d'origine (%7B / %7D).
var veridianBraceRestorer = strings.NewReplacer(
	string(veridianBraceOpenSentinel), "{",
	string(veridianBraceCloseSentinel), "}",
	"%EE%80%80", "%7B",
	"%EE%80%81", "%7D",
	"%ee%80%80", "%7b",
	"%ee%80%81", "%7d",
)

func veridianNeutralizeString(s string) string {
	if !strings.ContainsAny(s, "{}") {
		return s
	}
	return veridianBraceReplacer.Replace(s)
}

// veridianRestoreBraces remet les accolades à la place des sentinelles.
func veridianRestoreBraces(s string) string {
	return veridianBraceRestorer.Replace(s)
}

// veridianNeutralizeData renvoie une copie profonde de data dont toutes les
// valeurs texte ont leurs accolades neutralisées.
func veridianNeutralizeData(data MapOfAny) MapOfAny {
	if data == nil {
		return nil
	}
	out := make(MapOfAny, len(data))
	for k, v := range data {
		out[k] = veridianNeutralizeValue(v)
	}
	return out
}

func veridianNeutralizeValue(v interface{}) interface{} {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return veridianNeutralizeString(x)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return x
	case map[string]interface{}:
		m := make(map[string]interface{}, len(x))
		for k, e := range x {
			m[k] = veridianNeutralizeValue(e)
		}
		return m
	case MapOfAny:
		return veridianNeutralizeData(x)
	case []interface{}:
		s := make([]interface{}, len(x))
		for i, e := range x {
			s[i] = veridianNeutralizeValue(e)
		}
		return s
	case []string:
		s := make([]string, len(x))
		for i, e := range x {
			s[i] = veridianNeutralizeString(e)
		}
		return s
	case map[string]string:
		m := make(map[string]string, len(x))
		for k, e := range x {
			m[k] = veridianNeutralizeString(e)
		}
		return m
	}
	// Type composite inattendu (structure, pointeur, tranche typée) : repasse par
	// sa forme JSON, qui est ce que la compilation sérialise de toute façon.
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Struct, reflect.Ptr, reflect.Map, reflect.Slice, reflect.Array, reflect.Interface:
		b, err := json.Marshal(v)
		if err != nil {
			return v
		}
		var generic interface{}
		if err := json.Unmarshal(b, &generic); err != nil {
			return v
		}
		return veridianNeutralizeValue(generic)
	}
	return v
}

func veridianRestorePtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := veridianRestoreBraces(*s)
	return &v
}
