package veridian

// V80 (25/11/2026, fiche 91) : titulaire d'un accès de boîte. Posé : l'accès appartient à une personne et ne vaut que tant qu'elle a sa place
// dans l'espace. Vide : accès d'espace (boîte partagée, outil) ; tous les accès d'avant le restent, aucun n'est rattaché après coup. Additive.
func init() {
	Register(Spec{
		Number: 80, Lot: "Boites", Name: "titulaire_acces_boite",
		System: []string{`ALTER TABLE veridian_box_access ADD COLUMN IF NOT EXISTS holder_user_id UUID NULL`},
	})
}
