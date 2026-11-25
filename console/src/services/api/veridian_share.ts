// Deux blocs d'une même page qui demandent LA MÊME chose au même instant partagent une seule requête (25/11/2026 : le tableau de bord en
// envoyait 42, dont 21 identiques). Seulement pendant l'appel : rien n'est gardé, une relecture repart au serveur.
const inFlight = new Map<string, Promise<unknown>>()

export function shareInFlight<T>(key: string, run: () => Promise<T>): Promise<T> {
  const current = inFlight.get(key)
  if (current) return current as Promise<T>
  const started = run().finally(() => inFlight.delete(key))
  inFlight.set(key, started)
  return started
}
