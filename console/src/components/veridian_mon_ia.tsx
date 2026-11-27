/**
 * Veridian — « Mon IA » dans l'en-tête de la console (VER-203, CONTRAT-HUB §5.24.1, fiche 99).
 *
 * L'état vient du Hub par le serveur (jamais d'une clé ni d'un réglage saisi ici). Règle d'affichage : lib/veridian_mon_ia.
 * Marques communes à toutes les apps Veridian (la batterie système du Hub les lit) :
 *   - `[data-mon-ia="prete"]` : bouton « Mon IA » ; son clic ouvre un dialogue « Mon IA » ; le cadre n'est inséré qu'à ce moment ;
 *   - `[data-mon-ia="sans_moteur"]` : bouton « Relier mon IA » ; son clic ouvre `[data-carte="relier-mon-ia"]` avec le lien « Relier mon IA »
 *     (nouvel onglet) ; aucun cadre nulle part ; la carte porte aussi l'installation du connecteur MCP (veridian_connect_ai, fiche 83) ;
 *   - éteinte, 404, Hub injoignable : aucune marque.
 */

import { useState } from 'react'
import { Button, Divider, Modal, Typography } from 'antd'
import { RobotOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { api } from '../services/api/client'
import { VeridianInstallLinks } from './veridian_connect_ai'
import {
  affichageMonIA,
  urlCadreMonIA,
  urlRelierMonIA,
  type ReponseEtatMonIA
} from '../lib/veridian_mon_ia'

/** Dans un cadre (la console ouverte par une autre app), on n'imbrique rien. */
function enCadre(): boolean {
  try {
    return window.self !== window.top
  } catch {
    return true
  }
}

export function VeridianMonIA({ workspaceId }: { workspaceId: string | undefined }) {
  const { t } = useLingui()
  const [open, setOpen] = useState(false)

  const { data } = useQuery<ReponseEtatMonIA | null>({
    queryKey: ['veridian', 'ia-etat', workspaceId],
    queryFn: async () => {
      try {
        return await api.get<ReponseEtatMonIA>(
          `/api/veridian/ia/etat?workspace_id=${encodeURIComponent(workspaceId ?? '')}`
        )
      } catch {
        return null // 4xx, 5xx, réseau : on se tait
      }
    },
    enabled: !!workspaceId,
    staleTime: 60 * 1000, // le serveur garde aussi 60 s : jamais plus de 60 s de retard sur le Hub
    refetchOnWindowFocus: false,
    retry: false
  })

  if (!data || enCadre()) return null
  const affichage = affichageMonIA(data.etat)
  if (affichage === 'rien') return null
  const hub = data.hub_url ?? ''

  if (affichage === 'cadre') {
    const cadre = urlCadreMonIA(hub, data.tenant_id ?? '')
    if (!cadre) return null
    return (
      <span data-mon-ia="prete">
        <Button icon={<RobotOutlined aria-hidden />} onClick={() => setOpen(true)}>
          {t`Mon IA`}
        </Button>
        <Modal
          open={open}
          onCancel={() => setOpen(false)}
          footer={null}
          title={t`Mon IA`}
          width={820}
          destroyOnHidden
        >
          {open && (
            <iframe
              src={cadre}
              title={t`Mon IA`}
              data-testid="veridian-mon-ia-cadre"
              style={{ width: '100%', height: '70vh', border: 0 }}
              referrerPolicy="strict-origin"
            />
          )}
        </Modal>
      </span>
    )
  }

  const relier = urlRelierMonIA(hub)
  if (!relier) return null
  return (
    <span data-mon-ia="sans_moteur">
      <Button icon={<RobotOutlined aria-hidden />} onClick={() => setOpen(true)}>
        {t`Relier mon IA`}
      </Button>
      <Modal open={open} onCancel={() => setOpen(false)} footer={null} title={t`Relier mon IA`} destroyOnHidden>
        <div data-carte="relier-mon-ia">
          <Typography.Paragraph>
            {t`Votre IA travaille dans vos apps Veridian depuis son propre écran (Claude, ChatGPT, Mistral…) : reliez-la une fois, sans clé.`}
          </Typography.Paragraph>
          <VeridianInstallLinks />
          <Divider />
          <Button type="primary" href={relier} target="_blank" rel="noopener noreferrer">
            {t`Relier mon IA`}
          </Button>
        </div>
      </Modal>
    </span>
  )
}
