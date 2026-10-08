package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmailProviderVeridianProfileType(t *testing.T) {
	cases := []struct {
		name     string
		provider *EmailProvider
		want     string
	}{
		{"nil", nil, ""},
		{"smtp générique", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.relai.example"}}, VeridianProfileTypeSMTP},
		{"gmail mot de passe d'application", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.gmail.com"}}, VeridianProfileTypeGmailAppPassword},
		{"gmail oauth", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.gmail.com", AuthType: "oauth2", OAuth2Provider: "google"}}, VeridianProfileTypeGmailOAuth},
		{"oauth google sur un autre hote", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.example.test", AuthType: "oauth2", OAuth2Provider: "google"}}, VeridianProfileTypeGmailOAuth},
		{"fournisseur API", &EmailProvider{Kind: EmailProviderKindSES}, "ses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.provider.VeridianProfileType())
		})
	}
}

func TestVeridianBuildPlanObserved(t *testing.T) {
	rows := []VeridianPlanObservationRow{
		{ProfileID: "p1", SenderEmail: "Hello@Envoi.example", ProviderClass: "google", Accepted: 4},
		{ProfileID: "p1", SenderEmail: "hello@envoi.example", ProviderClass: "microsoft", Accepted: 2},
		{ProfileID: "p2", SenderEmail: "autre@envoi.example", ProviderClass: "google", Accepted: 3},
		{ProfileID: "", SenderEmail: "", ProviderClass: "google", Accepted: 9},
	}
	counters := []VeridianPlanCounterRow{
		{Kind: VeridianDailyQuotaKindProfile, Scope: "p1", Used: 7},
		{Kind: VeridianDailyQuotaKindProfile, Scope: "p2", Used: 99},
		{Kind: VeridianDailyQuotaKindWarmup, Scope: "envoi.example", Used: 11},
		{Kind: VeridianDailyQuotaKindProviderClass, Scope: "envoi.example", ProviderClass: "google", Used: 8},
	}
	obs := VeridianBuildPlanObserved("p1", rows, counters)
	assert.Equal(t, 6, obs.ProfileAccepted, "seulement les envois du profil")
	assert.Equal(t, 7, obs.ProfileReserved, "le compteur du profil, pas celui d'un autre")
	assert.Equal(t, 9, obs.DomainSent["envoi.example"], "le domaine compte tous les profils qui l'utilisent")
	assert.Equal(t, 6, obs.SenderSent["hello@envoi.example"], "adresses comptées en minuscules")
	assert.Equal(t, 7, obs.DomainClassSent["envoi.example"]["google"])
	assert.Equal(t, 11, obs.DomainReserved["envoi.example"])
	assert.Equal(t, 8, obs.DomainClassReserved["envoi.example"]["google"])
}

// Lot 4 (08/10/2026) : un mail transactionnel compte sur son profil, jamais dans les
// compteurs d'adresse, de domaine ou de classe de la chauffe et des plafonds.
func TestVeridianBuildPlanObservedKeepsTransactionalOutOfCommercialCounters(t *testing.T) {
	rows := []VeridianPlanObservationRow{
		{ProfileID: "nord", SenderEmail: "hello@nord.example", ProviderClass: "google", Accepted: 7},
		{ProfileID: "tx", SenderEmail: "no-reply@nord.example", ProviderClass: "google", MessageType: VeridianMessageTypeTransactional, Accepted: 40},
	}
	commercial := VeridianBuildPlanObserved("nord", rows, nil)
	assert.Equal(t, 7, commercial.ProfileAccepted)
	assert.Equal(t, 7, commercial.DomainSent["nord.example"], "le transactionnel du meme domaine ne gonfle pas la chauffe")
	assert.Equal(t, 7, commercial.DomainClassSent["nord.example"]["google"])
	transactional := VeridianBuildPlanObserved("tx", rows, nil)
	assert.Equal(t, 40, transactional.ProfileAccepted, "le volume transactionnel se lit sur son profil")
}
