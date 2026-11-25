// Dernier espace ouvert, par personne et par navigateur : simple confort, jamais un droit (l'espace est revérifié dans la liste de la personne).
const key = (userId: string) => `veridian:dernier-espace:${userId}`

export function rememberSpace(userId: string | undefined, workspaceId: string | undefined): void {
  if (!userId || !workspaceId) return
  try {
    localStorage.setItem(key(userId), workspaceId)
  } catch {
    // stockage refusé : la personne choisira
  }
}

export function recallSpace(userId: string | undefined): string | undefined {
  if (!userId) return undefined
  try {
    return localStorage.getItem(key(userId)) ?? undefined
  } catch {
    return undefined
  }
}
