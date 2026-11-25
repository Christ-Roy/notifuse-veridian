// Point de decision unique des droits de role d'espace (console). Le serveur fait foi ;
// la console ne fait que suivre. Roles : owner, admin (valeur API « admin », affiche
// « Veridian Admin »), member.
// - administre l'espace (tout, sauf ce qui reste au proprietaire) : owner OU admin ;
// - proprietaire seul : zone de danger (suppression de l'espace), transfert de propriete,
//   retrait ou modification d'un proprietaire.

export function isWorkspaceOwner(role?: string | null): boolean {
  return role === 'owner'
}

export function isWorkspaceAdministrator(role?: string | null): boolean {
  return role === 'owner' || role === 'admin'
}
