import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Empty, Grid, Result, Skeleton, Tabs, theme, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { useAuth, useWorkspacePermissions } from '../contexts/AuthContext'
import {
  mailErrorKind,
  mailService,
  type MailMessage,
  type MailSummary,
  type MailStreamEvent
} from '../services/api/veridian_mail'
import { useMailEvents } from '../components/mail/veridian_mail_hooks'
import { VeridianMailFilters } from '../components/mail/veridian_mail_filters'
import { MAIL_TIMEZONE, useMailLabels } from '../components/mail/veridian_mail_labels'
import { VeridianMailList } from '../components/mail/veridian_mail_list'
import { VeridianMailMessageView } from '../components/mail/veridian_mail_message_view'
import { mailKeys, useMailboxes } from '../components/mail/veridian_mail_queries'
import { VeridianMailTimeline } from '../components/mail/veridian_mail_timeline'
import {
  DEFAULT_NATURE,
  DEFAULT_STATE,
  handledParam,
  NATURE_KINDS,
  type InboxSearch,
  type NatureFilter,
  type StateFilter
} from '../components/mail/veridian_mail_rules'

const { Text, Title } = Typography

const PAGE_SIZE = 30

// « Boîte de réception » : tous les mails envoyés et reçus, lus en entier dans leur fil, comme
// dans un client IMAP. Liste à gauche (filtres, pagination par curseur), fiche à droite ; sur
// mobile la liste puis la fiche en plein écran. URL partageable (?message=ID&boite=...).
export function InboxPage() {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const screens = Grid.useBreakpoint()
  const isMobile = !screens.md
  const { workspaceId } = useParams({ strict: false }) as { workspaceId?: string }
  const search = useSearch({ strict: false }) as InboxSearch
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { workspaces } = useAuth()
  const { permissions, loading: loadingPermissions } = useWorkspacePermissions(workspaceId ?? '')
  const { errorText } = useMailLabels()
  const canRead = !!(permissions?.message_history?.read || permissions?.message_history?.write)
  const timezone = workspaces?.find((w) => w.id === workspaceId)?.settings?.timezone || MAIL_TIMEZONE

  const nature: NatureFilter = search.nature ?? DEFAULT_NATURE
  const etat: StateFilter = search.etat ?? DEFAULT_STATE
  const q = search.q ?? ''
  const boite = search.boite
  const ws = workspaceId ?? ''

  const setSearch = useCallback(
    (patch: Partial<InboxSearch>, replace = false) => {
      void navigate({
        search: ((prev: Record<string, unknown>) => ({ ...prev, ...patch })) as never,
        replace
      })
    },
    [navigate]
  )

  const mailboxesQuery = useMailboxes(workspaceId, canRead)
  const filters = useMemo(() => ({ boite, nature, etat, q }), [boite, nature, etat, q])

  const listQuery = useInfiniteQuery({
    queryKey: mailKeys.list(ws, filters),
    enabled: !!workspaceId && canRead,
    initialPageParam: '',
    retry: false,
    queryFn: ({ pageParam }) =>
      mailService.list({
        workspace_id: ws,
        mailbox: boite,
        kind: NATURE_KINDS[nature],
        handled: handledParam(etat),
        q: q || undefined,
        limit: PAGE_SIZE,
        cursor: pageParam || undefined
      }),
    getNextPageParam: (last) => last.next_cursor || undefined
  })
  const items: MailSummary[] = useMemo(() => listQuery.data?.pages.flatMap((p) => p.messages) ?? [], [listQuery.data])

  // Bureau : sans message choisi on affiche le premier de la liste (sans toucher à l'URL).
  const selectedId = search.message ?? (isMobile ? undefined : items[0]?.id)

  const messageQuery = useQuery({
    queryKey: mailKeys.message(ws, selectedId ?? ''),
    queryFn: () => mailService.get(ws, selectedId as string),
    enabled: !!workspaceId && canRead && !!selectedId,
    retry: false
  })
  const message = messageQuery.data
  const threadQuery = useQuery({
    queryKey: mailKeys.thread(ws, message?.thread_id ?? ''),
    queryFn: () => mailService.thread(ws, message?.thread_id as string),
    enabled: !!workspaceId && canRead && !!message?.thread_id,
    retry: false
  })

  // --- mises à jour : le message, sa ligne de liste et les compteurs restent d'accord
  const [busyId, setBusyId] = useState<string>()
  const applyUpdate = useCallback(
    (m: MailMessage) => {
      queryClient.setQueryData(mailKeys.message(ws, m.id), m)
      queryClient.setQueriesData<{ pages: Array<{ messages: MailSummary[]; next_cursor: string }>; pageParams: unknown[] }>(
        { queryKey: mailKeys.lists(ws) },
        (old) =>
          old && {
            ...old,
            pages: old.pages.map((p) => ({
              ...p,
              messages: p.messages.map((s) => (s.id === m.id ? { ...s, handled_at: m.handled_at, handled_by: m.handled_by, labels: m.labels } : s))
            }))
          }
      )
      void queryClient.invalidateQueries({ queryKey: mailKeys.mailboxes(ws) })
      void queryClient.invalidateQueries({ queryKey: mailKeys.lastReply(ws) })
      if (m.thread_id) void queryClient.invalidateQueries({ queryKey: mailKeys.thread(ws, m.thread_id) })
    },
    [queryClient, ws]
  )

  const toggleHandled = useCallback(
    async (item: MailSummary) => {
      setBusyId(item.id)
      try {
        applyUpdate(await mailService.update(ws, item.id, { handled: !item.handled_at }))
      } catch {
        void queryClient.invalidateQueries({ queryKey: mailKeys.lists(ws) })
      } finally {
        setBusyId(undefined)
      }
    },
    [applyUpdate, queryClient, ws]
  )

  // --- flux d'événements : un nouveau mail rafraîchit la liste, les compteurs et le fil ouvert
  const threadOpen = message?.thread_id
  const onEvent = useCallback(
    (e: MailStreamEvent) => {
      void queryClient.invalidateQueries({ queryKey: mailKeys.lists(ws) })
      void queryClient.invalidateQueries({ queryKey: mailKeys.mailboxes(ws) })
      void queryClient.invalidateQueries({ queryKey: mailKeys.lastReply(ws) })
      if (threadOpen && e.summary.thread_id === threadOpen) void queryClient.invalidateQueries({ queryKey: mailKeys.thread(ws, threadOpen) })
    },
    [queryClient, ws, threadOpen]
  )
  const streamState = useMailEvents(workspaceId, onEvent, canRead)

  // --- navigation au clavier : j / k dans la liste, Échap pour revenir (mobile)
  const itemsRef = useRef(items)
  useEffect(() => {
    itemsRef.current = items
  }, [items])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (!target || e.metaKey || e.ctrlKey || e.altKey) return
      const tag = target.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable) return
      if (e.key !== 'j' && e.key !== 'k') return
      const list = itemsRef.current
      if (list.length === 0) return
      const idx = list.findIndex((m) => m.id === selectedId)
      const next = e.key === 'j' ? Math.min(list.length - 1, idx + 1) : Math.max(0, idx < 0 ? 0 : idx - 1)
      if (list[next] && list[next].id !== selectedId) {
        e.preventDefault()
        setSearch({ message: list[next].id }, true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [selectedId, setSearch])

  const [panel, setPanel] = useState<'message' | 'conversation'>(search.onglet ?? 'message')
  const activePanel = search.onglet ?? panel

  if (!workspaceId) return <div>{t`Loading...`}</div>
  if (loadingPermissions) return <Skeleton active />
  if (!canRead) {
    return <Result status="403" title={t`Access denied`} subTitle={t`You need read access to the sending log to open the inbox.`} />
  }

  const filtered = nature !== DEFAULT_NATURE || etat !== DEFAULT_STATE || !!q || !!boite
  const resetFilters = () => setSearch({ nature: undefined, etat: undefined, q: undefined, boite: undefined, message: undefined })
  const mailboxes = mailboxesQuery.data ?? []
  const showDetail = !!selectedId && (!isMobile || !!search.message)
  const showList = !isMobile || !search.message

  const threadBlock = threadQuery.data && (
    <VeridianMailTimeline
      key={`${threadQuery.data.thread_id}-${activePanel}`}
      workspaceId={workspaceId}
      thread={threadQuery.data}
      currentId={message?.id}
      timezone={timezone}
      expandCurrent={activePanel === 'conversation'}
      onOpen={(id) => setSearch({ message: id, onglet: 'message' })}
    />
  )

  const detail = (() => {
    if (!selectedId) {
      return (
        <div style={{ padding: 48 }}>
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t`Choose a message to read it in full.`}>
            <Text type="secondary">{t`Keyboard: j and k move through the list.`}</Text>
          </Empty>
        </div>
      )
    }
    if (messageQuery.isLoading) return <Skeleton active paragraph={{ rows: 10 }} style={{ padding: 24 }} />
    if (messageQuery.isError || !message) {
      const text = errorText(mailErrorKind(messageQuery.error))
      return (
        <Result
          status={mailErrorKind(messageQuery.error) === 'not_found' ? '404' : 'warning'}
          title={text.title}
          subTitle={text.hint}
          extra={[
            <Button key="retry" type="primary" icon={<ReloadOutlined />} onClick={() => void messageQuery.refetch()}>
              {t`Retry`}
            </Button>,
            isMobile && (
              <Button key="back" onClick={() => setSearch({ message: undefined })}>
                {t`Back to the list`}
              </Button>
            )
          ]}
        />
      )
    }
    const conversationCount = threadQuery.data?.message_count ?? 1
    return (
      <Tabs
        destroyInactiveTabPane
        activeKey={activePanel}
        onChange={(k) => {
          setPanel(k as 'message' | 'conversation')
          setSearch({ onglet: k === 'conversation' ? 'conversation' : undefined }, true)
        }}
        tabBarStyle={{ padding: '0 20px', margin: 0 }}
        items={[
          {
            key: 'message',
            label: t`Message`,
            children: (
              <div style={{ padding: isMobile ? 12 : 20 }}>
                <VeridianMailMessageView
                  workspaceId={workspaceId}
                  message={message}
                  mailboxes={mailboxes}
                  timezone={timezone}
                  onChange={applyUpdate}
                  onReplySent={() => {
                    void queryClient.invalidateQueries({ queryKey: mailKeys.thread(ws, message.thread_id) })
                  }}
                  onBack={isMobile ? () => setSearch({ message: undefined, onglet: undefined }) : undefined}
                  footer={
                    threadQuery.data && threadQuery.data.message_count > 1 ? (
                      <section aria-label={t`In this conversation`}>
                        <Title level={5} style={{ margin: '4px 0 12px' }}>
                          {t`In this conversation`}
                        </Title>
                        {threadBlock}
                      </section>
                    ) : null
                  }
                />
              </div>
            )
          },
          {
            key: 'conversation',
            label: conversationCount > 1 ? t`Conversation (${conversationCount})` : t`Conversation`,
            children: (
              <div style={{ padding: isMobile ? 12 : 20 }}>
                {threadQuery.isLoading && <Skeleton active paragraph={{ rows: 6 }} />}
                {threadQuery.isError && (
                  <Alert
                    type="warning"
                    showIcon
                    message={errorText(mailErrorKind(threadQuery.error)).title}
                    action={
                      <Button size="small" onClick={() => void threadQuery.refetch()}>
                        {t`Retry`}
                      </Button>
                    }
                  />
                )}
                {threadBlock}
              </div>
            )
          }
        ]}
      />
    )
  })()

  return (
    <div
      data-testid="inbox-page"
      style={{
        display: 'flex',
        height: isMobile ? 'auto' : 'calc(100vh - 64px - 48px - 8px)',
        minHeight: isMobile ? 'calc(100vh - 140px)' : 560,
        border: `1px solid ${token.colorBorderSecondary}`,
        borderRadius: 10,
        background: token.colorBgContainer,
        overflow: 'hidden'
      }}
    >
      {showList && (
        <aside
          aria-label={t`Message list`}
          style={{
            flex: isMobile ? '1 1 100%' : '0 0 clamp(340px, 34%, 440px)',
            minWidth: 0,
            display: 'flex',
            flexDirection: 'column',
            borderRight: isMobile ? 'none' : `1px solid ${token.colorBorderSecondary}`
          }}
        >
          <div style={{ padding: 14, borderBottom: `1px solid ${token.colorBorderSecondary}` }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
              <Title level={4} style={{ margin: 0 }}>
                {t`Inbox`}
              </Title>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
                {streamState === 'retry' && (
                  <Text type="secondary" style={{ fontSize: 12 }} data-testid="stream-retry">
                    {t`Live updates paused, reconnecting…`}
                  </Text>
                )}
                <Button
                  type="text"
                  size="small"
                  icon={<ReloadOutlined />}
                  aria-label={t`Refresh the list`}
                  onClick={() => {
                    void listQuery.refetch()
                    void mailboxesQuery.refetch()
                  }}
                />
              </span>
            </div>
            <VeridianMailFilters
              mailboxes={mailboxes}
              boite={boite}
              nature={nature}
              etat={etat}
              q={q}
              onBoite={(id) => setSearch({ boite: id, message: undefined })}
              onNature={(n) => setSearch({ nature: n === DEFAULT_NATURE ? undefined : n, message: undefined })}
              onEtat={(e) => setSearch({ etat: e === DEFAULT_STATE ? undefined : e, message: undefined })}
              onQuery={(value) => setSearch({ q: value || undefined, message: undefined }, true)}
            />
          </div>
          <div style={{ flex: 1, overflowY: isMobile ? 'visible' : 'auto' }}>
            <VeridianMailList
              items={items}
              selectedId={selectedId}
              loading={listQuery.isLoading}
              error={listQuery.error}
              hasMore={!!listQuery.hasNextPage}
              loadingMore={listQuery.isFetchingNextPage}
              filtered={filtered}
              timezone={timezone}
              busyId={busyId}
              onSelect={(id) => setSearch({ message: id, onglet: undefined })}
              onToggleHandled={(item) => void toggleHandled(item)}
              onLoadMore={() => void (listQuery.error ? listQuery.refetch() : listQuery.fetchNextPage())}
              onRetry={() => void listQuery.refetch()}
              onResetFilters={resetFilters}
            />
          </div>
        </aside>
      )}
      {showDetail && (
        <main aria-label={t`Message`} style={{ flex: 1, minWidth: 0, overflowY: isMobile ? 'visible' : 'auto' }}>
          {detail}
        </main>
      )}
    </div>
  )
}
