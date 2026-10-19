/**
 * Rapport d'import CSV exact (audit UI 19/10/2026, E1).
 *
 * Le serveur (BatchImportContacts) ne rend PAS les operations dans l'ordre d'envoi : les
 * contacts invalides sortent en premier, les valides ensuite, et les doublons d'un meme lot
 * sont dedoublonnes (la derniere occurrence est gardee). Rattacher l'operation i a la
 * ligne i, comme le faisait l'ecran, attache une erreur a la mauvaise ligne et fait
 * disparaitre une ligne sans message. Ici chaque operation est rattachee a SA ligne par
 * l'adresse e-mail, et chaque ligne du fichier finit exactement dans « importee » ou dans
 * « ignoree avec une raison » : importees + ignorees = lignes du fichier.
 */

export interface CsvSentRow {
  /** Numero de ligne dans le fichier (l'en-tete est la ligne 1). */
  line: number
  email: string
}

export interface CsvBatchOperation {
  email?: string
  action: string
  error?: string
}

export interface CsvImportFailure {
  line: number
  email: string
  error: string
}

export interface BatchReconciliation {
  succeeded: number
  failures: CsvImportFailure[]
}

export const DUPLICATE_IN_BATCH_ERROR = 'duplicate: a later line of the file has the same email address'

const norm = (email?: string): string => (email || '').trim().toLowerCase()

/** Retire le « invalid contact at index 3: » technique : l'index n'est pas une ligne du fichier. */
export const stripServerIndex = (raw: string): string =>
  raw
    .replace(/^invalid contact at index \d+:\s*/i, '')
    .replace(/^failed to upsert contact at index \d+:\s*/i, '')

export function reconcileBatch(
  sent: CsvSentRow[],
  operations: CsvBatchOperation[],
  batchError?: string
): BatchReconciliation {
  if (batchError) {
    return {
      succeeded: 0,
      failures: sent.map((r) => ({ line: r.line, email: r.email, error: batchError }))
    }
  }

  const linesByEmail = new Map<string, CsvSentRow[]>()
  for (const row of sent) {
    const key = norm(row.email)
    const list = linesByEmail.get(key) || []
    list.push(row)
    linesByEmail.set(key, list)
  }
  const opsByEmail = new Map<string, CsvBatchOperation[]>()
  for (const op of operations) {
    const key = norm(op.email)
    const list = opsByEmail.get(key) || []
    list.push(op)
    opsByEmail.set(key, list)
  }

  let succeeded = 0
  const failures: CsvImportFailure[] = []

  for (const [key, rows] of linesByEmail) {
    const ops = opsByEmail.get(key) || []
    // Le serveur garde la derniere occurrence : les k operations vont aux k dernieres lignes.
    const offset = Math.max(rows.length - ops.length, 0)
    for (let i = 0; i < offset; i++) {
      failures.push({
        line: rows[i].line,
        email: rows[i].email,
        error: DUPLICATE_IN_BATCH_ERROR
      })
    }
    for (let j = 0; j < rows.length - offset; j++) {
      const op = ops[j]
      const row = rows[offset + j]
      if (op.action === 'error') {
        failures.push({
          line: row.line,
          email: row.email,
          error: stripServerIndex(op.error || 'Unknown error')
        })
      } else {
        succeeded++
      }
    }
  }

  failures.sort((a, b) => a.line - b.line)
  return { succeeded, failures }
}

/**
 * Prepare les lignes lues par Papa : retire les lignes vides de FIN de fichier (artefact du
 * saut de ligne final, pas des donnees) et garde le numero de ligne physique de chacune.
 */
export function trimTrailingBlankRows(rows: string[][]): string[][] {
  let end = rows.length
  while (end > 0 && rows[end - 1].every((cell) => (cell ?? '').trim() === '')) end--
  return rows.slice(0, end)
}

export const isBlankRow = (row: string[]): boolean => row.every((cell) => (cell ?? '').trim() === '')
