package mailapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// enveloppe d'intermediaire SANS Flush, mais qui expose Unwrap (cas du journal d'acces avant le 19/11/2026).
type sansFlush struct{ http.ResponseWriter }

func (s sansFlush) Unwrap() http.ResponseWriter { return s.ResponseWriter }

type opaque struct{ http.ResponseWriter }

func TestFlusherOf_TraverseLesEnveloppes(t *testing.T) {
	rec := httptest.NewRecorder()
	assert.NotNil(t, flusherOf(rec), "reponse directe")
	f := flusherOf(sansFlush{sansFlush{rec}})
	if assert.NotNil(t, f, "deux enveloppes sans Flush mais avec Unwrap") {
		f.Flush()
		assert.True(t, rec.Flushed)
	}
	assert.Nil(t, flusherOf(opaque{rec}), "enveloppe opaque : refus explicite plutot qu'un flux muet")
}
