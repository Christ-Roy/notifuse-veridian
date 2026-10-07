import type { AnalyticsQuery, AnalyticsResponse } from '../../services/api/analytics'

// Veridian — séries des métriques email.
//
// Chaque mesure se groupe et se borne sur SA date d'événement, JAMAIS sur
// created_at (date de mise en file / d'inscription) : un message créé le 30/09
// et envoyé le 06/10 compte le 06/10. Envoyés -> sent_at, rejets -> bounced_at,
// plaintes -> complained_at, désinscriptions -> unsubscribed_at, échecs ->
// failed_at. Le moteur analytics n'accepte qu'une dimension temporelle par
// requête : une requête par famille, fusionnées ensuite par jour.

export type MessageTypeFilter = 'all' | 'broadcasts' | 'transactional'

export interface EmailSeriesDef {
  dimension: string
  measures: string[]
}

export const EMAIL_SERIES: EmailSeriesDef[] = [
  { dimension: 'sent_at', measures: ['count_sent'] },
  {
    dimension: 'bounced_at',
    measures: ['count_bounced', 'count_bounced_hard', 'count_bounced_soft']
  },
  { dimension: 'complained_at', measures: ['count_complained'] },
  { dimension: 'unsubscribed_at', measures: ['count_unsubscribed'] },
  { dimension: 'failed_at', measures: ['count_failed'] }
]

// Mesure annexe (best-effort côté composant) : exclusions volontaires, datées par
// failed_at comme count_failed.
export const EXCLUDED_SERIES: EmailSeriesDef = {
  dimension: 'failed_at',
  measures: ['count_failed_excluded']
}

// Colonne temporelle de la réponse fusionnée (dimension synthétique « event_date »).
export const MERGED_TIME_DIMENSION = 'event_date'
export const MERGED_TIME_FIELD = `${MERGED_TIME_DIMENSION}_day`

export const buildSeriesQuery = (
  def: EmailSeriesDef,
  filter: MessageTypeFilter,
  timeRange: [string, string],
  timezone: string
): AnalyticsQuery => {
  const query: AnalyticsQuery = {
    schema: 'message_history',
    measures: def.measures,
    dimensions: [],
    timezone,
    timeDimensions: [{ dimension: def.dimension, granularity: 'day', dateRange: timeRange }],
    filters: []
  }
  if (filter === 'broadcasts') {
    query.filters?.push({ member: 'broadcast_id', operator: 'set', values: [] })
  } else if (filter === 'transactional') {
    query.filters?.push({ member: 'broadcast_id', operator: 'notSet', values: [] })
  }
  return query
}

const toNumber = (value: unknown): number => {
  if (typeof value === 'number') return value
  if (typeof value === 'string') {
    const parsed = parseFloat(value)
    return isNaN(parsed) ? 0 : parsed
  }
  return 0
}

// Fusionne les réponses (une par famille) en UNE série par jour, triée. Le jour
// est la clé (10 premiers caractères de la date renvoyée par le moteur).
export const mergeSeries = (
  results: Array<{ def: EmailSeriesDef; response: AnalyticsResponse }>
): AnalyticsResponse => {
  const byDay = new Map<string, Record<string, unknown>>()
  const allMeasures = results.flatMap((r) => r.def.measures)

  for (const { def, response } of results) {
    const timeField = `${def.dimension}_day`
    for (const row of response.data || []) {
      const raw = row[timeField]
      if (raw === undefined || raw === null) continue
      const day = String(raw).slice(0, 10)
      let merged = byDay.get(day)
      if (!merged) {
        merged = { [MERGED_TIME_FIELD]: raw }
        for (const m of allMeasures) merged[m] = 0
        byDay.set(day, merged)
      }
      for (const m of def.measures) merged[m] = toNumber(merged[m]) + toNumber(row[m])
    }
  }

  const data = [...byDay.keys()].sort().map((day) => byDay.get(day) as Record<string, unknown>)
  return { data, meta: { total: data.length, query: '', params: [] } }
}
