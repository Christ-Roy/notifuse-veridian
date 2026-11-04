package relaycert

import (
	"context"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/relaysettings"
)

// SettingsParams lit la configuration du certificat dans les réglages à chaque décision
// (repli sur l'environnement quand un réglage est vide, voir relaysettings).
func SettingsParams(repo domain.SettingRepository, secretKey string, lookup relaysettings.Lookup) ParamsFunc {
	return func(ctx context.Context) (Params, error) {
		c, err := relaysettings.ReadCert(ctx, repo, secretKey, lookup)
		if err != nil {
			return Params{}, err
		}
		return Params{Managed: c.Managed, Email: c.Email, Directory: c.Directory, DirectoryURL: c.DirectoryURL, CFToken: c.CFToken}, nil
	}
}

// LegoIssuerFactory émet avec lego pour la configuration courante.
func LegoIssuerFactory(onCleanupError func(error)) IssuerFunc {
	return func(p Params) Issuer {
		return LegoIssuer{DirectoryURL: p.DirectoryURL, CFToken: p.CFToken, OnCleanupError: onCleanupError}
	}
}
