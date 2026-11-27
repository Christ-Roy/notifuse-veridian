// Recherche de modèles par nom dans la liste (reprise amont 35.0, « Search templates by name »).
// Filtre côté client sur la liste déjà chargée : insensible à la casse, sous-chaîne, espaces de bord ignorés.

export function filterTemplatesByName<T extends { name: string }>(templates: T[], query: string): T[] {
  const needle = query.trim().toLowerCase()
  if (needle === '') return templates
  return templates.filter((tpl) => tpl.name.toLowerCase().includes(needle))
}
