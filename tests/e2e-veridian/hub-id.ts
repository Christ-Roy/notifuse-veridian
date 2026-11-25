// Une personne du Hub est un UUID (CONTRAT-HUB 5.19.7). Même graine = même personne.
import * as crypto from 'crypto';

export function hubId(seed: string): string {
  const h = crypto.createHash('sha256').update(seed).digest('hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-4${h.slice(13, 16)}-8${h.slice(17, 20)}-${h.slice(20, 32)}`;
}
