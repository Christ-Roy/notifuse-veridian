// « Voir le contact » depuis un message de la boîte (27/11/2026, Robert : « pourquoi on n'a pas de bouton pour voir le profil
// du contact et avoir ses coordonnées ? »). Ouvre la fiche complète de la page Contacts (coordonnées, entreprise, champs
// personnalisés, listes, segments, historique des envois, chronologie) sans quitter la boîte. Le contact est celui que le
// serveur a rattaché au message (contact_email) : l'expéditeur d'une réponse, le destinataire d'un envoi.
import { UserOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { ContactDetailsDrawer } from '../contacts/ContactDetailsDrawer'
import { useOptionalAuth } from '../../contexts/AuthContext'
import { listsApi } from '../../services/api/list'
import { listSegments } from '../../services/api/segment'

export function MailContactButton({ workspaceId, email }: { workspaceId: string; email: string | undefined }) {
  const { t } = useLingui()
  // Hors session (aperçus, tests sans fournisseur) : pas d'espace, donc pas de bouton.
  const workspace = useOptionalAuth()?.workspaces.find((w) => w.id === workspaceId)
  // Noms des listes et des segments dans la fiche ; lus une fois par espace (mêmes clés que la page Contacts).
  const { data: lists } = useQuery({
    queryKey: ['lists', workspaceId],
    queryFn: () => listsApi.list({ workspace_id: workspaceId }),
    enabled: !!workspace && !!email
  })
  const { data: segments } = useQuery({
    queryKey: ['segments', workspaceId],
    queryFn: () => listSegments({ workspace_id: workspaceId, with_count: true }),
    enabled: !!workspace && !!email
  })
  if (!workspace || !email) return null
  return (
    <span data-testid="mail-contact-button">
      <ContactDetailsDrawer
        key={email}
        workspace={workspace}
        contactEmail={email}
        lists={lists?.lists || []}
        segments={segments?.segments || []}
        buttonProps={{ icon: <UserOutlined />, buttonContent: t`View contact` }}
      />
    </span>
  )
}
