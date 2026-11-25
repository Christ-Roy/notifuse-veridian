package veridian

// V81 (25/11/2026, fiche 92) : un accès de boîte se désactive et se réactive (même mot de passe), à côté de la révocation, définitive. Additive.
func init() {
	Register(Spec{
		Number: 81, Lot: "Boites", Name: "acces_boite_desactive",
		System: []string{`ALTER TABLE veridian_box_access ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ NULL`},
	})
}
