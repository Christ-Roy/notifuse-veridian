package http

// === Veridian patch — perf-ui-baseline (ticket 2026-05-22) ===
//
// VeridianStaticAssetsFilter est un middleware applique au handler GLOBAL
// (cf. app.go, meme pattern que VeridianPaywallPathFilter). Il optimise le
// serving des assets statiques de la console SPA sans patcher le handler
// upstream root_handler.go (convention CLAUDE.md : jamais toucher un
// fichier upstream).
//
// Probleme corrige : la console Vite produit un bundle index-<hash>.js de
// ~6.3 MB. Le http.FileServer upstream le sert NON COMPRESSE et sans
// Cache-Control → le client retelecharge 6.3 MB a chaque navigation. Sur
// fibre c'est 1.2 s ; en 4G c'est ~10 s.
//
// Ce middleware, pour les requetes /console/assets/* :
//   1. Pose Cache-Control: public, max-age=31536000, immutable sur les
//      assets au nom cache-bustable (contiennent un hash de contenu) —
//      le navigateur ne les redemande JAMAIS apres le 1er chargement.
//   2. Compresse la reponse en gzip a la volee si le client l'accepte
//      (Accept-Encoding: gzip) — transfert divise par ~3.5x sur du JS/CSS.
//      Le resultat compresse est mis en cache memoire : les assets sont
//      immuables entre 2 deploys, on ne recompresse pas a chaque hit.
//
// Choix gzip (pas brotli) : Go a compress/gzip en stdlib, brotli imposerait
// une dependance tierce (andybalholm/brotli) pour ~15 % de gain en plus —
// pas rentable au trafic actuel. Si le trafic grossit, ajouter brotli est
// un ticket de 30 min (etendre encodingFor + le cache).
//
// Tout ce qui n'est pas /console/assets/* passe direct au handler suivant.

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// veridianHashedAssetRe reconnait un asset au nom cache-bustable : un hash
// Vite (>=8 chars alphanum, +/_/-) juste avant l'extension. Ex :
// index-B7KYMJSS.js, index-BdqHGIYi.css, fr-D09p1dB3.js.
var veridianHashedAssetRe = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.(js|css)$`)

// veridianCompressibleExt liste les extensions qui valent le coup d'etre
// gzippees. Les .png/.woff2 sont deja compresses, gzip ne ferait que
// bruler du CPU pour ~0 gain.
var veridianCompressibleExt = map[string]bool{
	".js":   true,
	".css":  true,
	".svg":  true,
	".json": true,
	".map":  true,
}

// veridianGzipCache memorise les assets gzippes par chemin de requete.
// Les assets ont un hash dans le nom : une URL donnee = un contenu fige.
// Le cache se vide naturellement au redemarrage (= apres chaque deploy,
// ou les hash changent de toute facon).
type veridianGzipCache struct {
	mu      sync.RWMutex
	entries map[string][]byte
}

func (c *veridianGzipCache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.entries[key]
	return b, ok
}

func (c *veridianGzipCache) put(key string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = b
}

// VeridianStaticAssetsFilter retourne un middleware qui optimise le serving
// des assets console. A appliquer au handler global dans app.go.
func VeridianStaticAssetsFilter(log logger.Logger) func(http.Handler) http.Handler {
	cache := &veridianGzipCache{entries: make(map[string][]byte)}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Seuls les assets de la console nous interessent. Tout le
			// reste (API, blog, notification-center, index.html SPA)
			// passe direct.
			if !strings.HasPrefix(r.URL.Path, "/console/assets/") {
				next.ServeHTTP(w, r)
				return
			}

			ext := veridianExtOf(r.URL.Path)

			// 1. Cache-Control long sur les assets au nom hashe : le hash
			// change a chaque rebuild, donc l'URL est cache-bustable —
			// le navigateur peut les garder un an.
			if veridianHashedAssetRe.MatchString(r.URL.Path) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}

			// 2. Compression gzip a la volee si applicable. Sinon, on
			// laisse le FileServer upstream servir le fichier brut.
			if !veridianCompressibleExt[ext] || !veridianClientAcceptsGzip(r) {
				next.ServeHTTP(w, r)
				return
			}

			// Cache hit : on a deja la version gzippee de cet asset.
			if gz, ok := cache.get(r.URL.Path); ok {
				veridianWriteGzip(w, gz)
				return
			}

			// Cache miss : on capture la reponse du handler upstream,
			// on la gzippe, on la met en cache et on la sert.
			rec := &veridianResponseRecorder{
				header: make(http.Header),
				status: http.StatusOK,
			}
			next.ServeHTTP(rec, r)

			// Si le handler upstream n'a pas renvoye un 200 (ex : 404
			// fallback SPA, 405), on relaie la reponse telle quelle sans
			// compresser — ne pas masquer une erreur.
			if rec.status != http.StatusOK {
				veridianReplayRecorded(w, rec)
				return
			}

			var buf bytes.Buffer
			gzw := gzip.NewWriter(&buf)
			if _, err := gzw.Write(rec.body.Bytes()); err != nil {
				// Compression ratee : on sert le brut, pas de cache.
				log.WithField("path", r.URL.Path).WithField("error", err).
					Warn("veridian static: gzip write failed, serving uncompressed")
				veridianReplayRecorded(w, rec)
				return
			}
			if err := gzw.Close(); err != nil {
				log.WithField("path", r.URL.Path).WithField("error", err).
					Warn("veridian static: gzip close failed, serving uncompressed")
				veridianReplayRecorded(w, rec)
				return
			}

			gz := buf.Bytes()
			cache.put(r.URL.Path, gz)

			// Reporter le Content-Type devine par le FileServer upstream.
			if ct := rec.header.Get("Content-Type"); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			veridianWriteGzip(w, gz)
		})
	}
}

// veridianExtOf retourne l'extension (avec le point) du chemin, en
// minuscules. "" si aucune.
func veridianExtOf(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return strings.ToLower(path[i:])
}

// veridianClientAcceptsGzip indique si le client annonce gzip dans
// Accept-Encoding.
func veridianClientAcceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// veridianWriteGzip ecrit un corps deja gzippe avec les headers qui vont
// bien. Vary: Accept-Encoding evite qu'un cache intermediaire serve la
// version gzip a un client qui ne la comprend pas.
func veridianWriteGzip(w http.ResponseWriter, gz []byte) {
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Length", strconv.Itoa(len(gz)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(gz)
}

// veridianResponseRecorder capture la reponse d'un handler en aval pour
// pouvoir la gzipper avant de l'envoyer au client reel.
type veridianResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (rec *veridianResponseRecorder) Header() http.Header { return rec.header }

func (rec *veridianResponseRecorder) WriteHeader(status int) {
	if rec.wrote {
		return
	}
	rec.status = status
	rec.wrote = true
}

func (rec *veridianResponseRecorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	return rec.body.Write(b)
}

// veridianReplayRecorded rejoue une reponse capturee telle quelle vers le
// client reel (cas non compressible / erreur upstream).
func veridianReplayRecorded(w http.ResponseWriter, rec *veridianResponseRecorder) {
	for k, vs := range rec.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body.Bytes())
}
