package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parisLoc(t *testing.T) *time.Location {
	loc, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)
	return loc
}

func TestVeridianDayAt_ParisSummerDayStartsAt2200UTC(t *testing.T) {
	day := VeridianDayAt(time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), parisLoc(t))
	assert.Equal(t, "2026-10-08", day.LabelDate())
	assert.Equal(t, time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC), day.Start)
	assert.Equal(t, time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), day.End)
	assert.Equal(t, 24*time.Hour, day.End.Sub(day.Start))
}

// Ancien comportement : minuit UTC. A 23h30 UTC le 8 octobre il est 01h30 le 9 a Paris :
// le compteur du 8 est ferme depuis 2 h, plus aucun plafond ne doit se souvenir du 8.
func TestVeridianDayAt_ParisMidnightFlipsBeforeUTCMidnight(t *testing.T) {
	loc := parisLoc(t)
	before := VeridianDayAt(time.Date(2026, 10, 8, 21, 59, 59, 0, time.UTC), loc) // 23:59:59 Paris
	after := VeridianDayAt(time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), loc)    // 00:00:00 Paris
	assert.Equal(t, "2026-10-08", before.LabelDate())
	assert.Equal(t, "2026-10-09", after.LabelDate())
	assert.Equal(t, before.End, after.Start, "aucun trou et aucun recouvrement entre deux jours")
}

func TestVeridianDayAt_DaylightSavingDaysLast23And25Hours(t *testing.T) {
	loc := parisLoc(t)
	spring := VeridianDayAt(time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC), loc)
	assert.Equal(t, 23*time.Hour, spring.End.Sub(spring.Start), "29 mars 2026 : passage a l'heure d'ete")
	assert.Equal(t, "2026-03-29", spring.LabelDate())
	autumn := VeridianDayAt(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), loc)
	assert.Equal(t, 25*time.Hour, autumn.End.Sub(autumn.Start), "25 octobre 2026 : retour a l'heure d'hiver")
	assert.Equal(t, "2026-10-25", autumn.LabelDate())

	// Les jours voisins s'enchainent sans trou ni recouvrement autour du changement.
	prev := VeridianDayAt(spring.Start.Add(-time.Second), loc)
	next := VeridianDayAt(spring.End, loc)
	assert.Equal(t, "2026-03-28", prev.LabelDate())
	assert.Equal(t, prev.End, spring.Start)
	assert.Equal(t, "2026-03-30", next.LabelDate())
	assert.Equal(t, spring.End, next.Start)
	// 30 mars 2026 = lendemain a l'heure d'ete (UTC+2) : minuit Paris = 22h00 UTC la veille.
	assert.Equal(t, time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC), next.Start)
}

func TestVeridianDayAt_NilLocationIsUTC(t *testing.T) {
	day := VeridianDayAt(time.Date(2026, 10, 8, 23, 59, 0, 0, time.UTC), nil)
	assert.Equal(t, "2026-10-08", day.LabelDate())
	assert.Equal(t, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), day.Start)
	assert.Equal(t, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), day.End)
}

func TestVeridianDayLocation_Precedence(t *testing.T) {
	win := func(tz string) *VeridianSendingWindow {
		return &VeridianSendingWindow{StartHour: 8, EndHour: 19, Timezone: tz}
	}
	ws := &Workspace{Settings: WorkspaceSettings{Timezone: "Asia/Tokyo", VeridianSendingWindow: win("America/New_York")}}
	provider := &EmailProvider{VeridianSendingWindow: win("Europe/Paris")}

	assert.Equal(t, "Europe/Paris", VeridianDayLocation(ws, provider).String(), "fenetre du profil d'abord")
	assert.Equal(t, "America/New_York", VeridianDayLocation(ws, &EmailProvider{}).String(), "puis fenetre du workspace")
	assert.Equal(t, "Asia/Tokyo", VeridianDayLocation(&Workspace{Settings: WorkspaceSettings{Timezone: "Asia/Tokyo"}}, nil).String(), "puis fuseau du workspace")
	assert.Equal(t, "UTC", VeridianDayLocation(&Workspace{}, nil).String(), "puis UTC")
	assert.Equal(t, "UTC", VeridianDayLocation(nil, nil).String())
	// Fenetre sans fuseau : celui du workspace. Nom invalide : niveau suivant, jamais d'erreur.
	assert.Equal(t, "Asia/Tokyo", VeridianDayLocation(&Workspace{Settings: WorkspaceSettings{Timezone: "Asia/Tokyo"}}, &EmailProvider{VeridianSendingWindow: win("")}).String())
	assert.Equal(t, "Asia/Tokyo", VeridianDayLocation(&Workspace{Settings: WorkspaceSettings{Timezone: "Asia/Tokyo"}}, &EmailProvider{VeridianSendingWindow: win("Nowhere/Land")}).String())
	assert.Equal(t, "UTC", VeridianDayLocation(&Workspace{Settings: WorkspaceSettings{Timezone: "Nowhere/Land"}}, nil).String())
}
