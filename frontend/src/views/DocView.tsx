import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Banner, Button, Spin, Typography } from '@douyinfe/semi-ui'
import { IconFolder } from '@douyinfe/semi-icons'
import type { Doc, Work } from '../types'
import { getDoc, getWork, putDocContent } from '../lib/api'
import { useAppStore } from '../lib/store'
import { UNKNOWN_WORK_ID, folderPath, workPath } from '../lib/routes'
import OutlinePane from '../components/shell/OutlinePane'
import MarkdownEditor from '../components/editor/MarkdownEditor'
import { parseOutline } from '../lib/mdOutline'
import { useLiveRefresh } from '../lib/useLiveRefresh'

const { Text } = Typography

export default function DocView() {
  const { id: workIdParam, docId } = useParams()
  const nav = useNavigate()
  const { contentStamp, lastCommitted, setAiPanelOpen, nodes, docMode, me, staging } = useAppStore()
  const [work, setWork] = useState<Work | null>(null)
  const [doc, setDoc] = useState<Doc | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [outlineMd, setOutlineMd] = useState('')

  // Sentinel `-` means the work UUID is unknown (e.g. search hit → doc deep link)
  const workId = workIdParam && workIdParam !== UNKNOWN_WORK_ID ? workIdParam : null

  const load = useCallback(async (opts?: { silent?: boolean }) => {
    if (!docId) return
    if (!opts?.silent) {
      setLoading(true)
      setError(null)
    }
    try {
      // Resolve doc solely by its UUID — no tree/slug map needed
      const dr = await getDoc(docId)
      if (!dr?.doc) throw new Error('资料不存在或已被删除')
      setDoc(dr.doc)
      setOutlineMd(dr.doc.contentMd ?? '')

      // Optionally attach the parent work (by route id, or from doc.folderOf)
      const tryWorkId = workId ?? dr.doc.folderOf
      if (tryWorkId) {
        try {
          const wr = await getWork(tryWorkId)
          setWork(wr.work)
        } catch {
          setWork(null)
        }
      } else {
        setWork(null)
      }
    } catch (e) {
      if (opts?.silent) return
      setError(e instanceof Error ? e.message : '加载资料失败')
      setDoc(null)
    } finally {
      if (!opts?.silent) setLoading(false)
    }
  }, [docId, workId])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (!doc || !lastCommitted) return
    if (lastCommitted.targetId === doc.id) void load()
  }, [contentStamp, lastCommitted, doc, load])

  const headings = useMemo(() => parseOutline(outlineMd), [outlineMd])

  const aiWriting = !!doc && staging?.targetId === doc.id
  useLiveRefresh(aiWriting, useCallback(() => load({ silent: true }), [load]))

  const linkedTitles = useMemo(
    () =>
      (doc?.links ?? []).map((lid) => ({
        id: lid,
        title: nodes.find((n) => n.id === lid)?.title ?? lid.slice(0, 8),
      })),
    [doc, nodes],
  )

  const backFolder = useCallback(() => {
    const wid = work?.id ?? workId
    if (wid) nav(folderPath(wid))
    else nav('/')
  }, [work, workId, nav])

  const onSave = useCallback(
    async (body: Parameters<typeof putDocContent>[1]) => {
      if (!doc) throw new Error('资料未加载')
      setSaving(true)
      try {
        const res = await putDocContent(doc.id, body)
        setDoc((d) =>
          d ? { ...d, contentMd: body.contentMd, contentVer: res.contentVer } : d,
        )
        setOutlineMd(body.contentMd)
        return res
      } finally {
        setSaving(false)
      }
    },
    [doc],
  )

  if (loading && !doc) {
    return <Spin style={{ display: 'block', margin: '64px auto' }} />
  }

  if (error || !doc) {
    return (
      <div style={{ padding: 32 }}>
        <Banner type="danger" description={error ?? '资料不存在'} />
        <Button style={{ marginTop: 16 }} onClick={backFolder}>
          返回资料夹
        </Button>
      </div>
    )
  }

  return (
    <div className="work-view">
      <OutlinePane
        headings={headings}
        title={doc.title}
        contentKey={`${doc.contentVer}:${outlineMd.length}`}
      />
      <div className="work-content">
        <MarkdownEditor
          key={doc.id}
          title={doc.title}
          updatedAt={doc.updatedAt}
          contentMd={doc.contentMd}
          contentVer={doc.contentVer}
          saving={saving}
          mode={!me.authed || docMode === 'read' ? 'read' : 'edit'}
          onAiWrite={me.authed ? () => setAiPanelOpen(true) : undefined}
          actions={me.authed ? (
            <Button
              theme="borderless"
              type="tertiary"
              size="small"
              icon={<IconFolder />}
              onClick={backFolder}
            >
              所属资料夹
            </Button>
          ) : undefined}
          onSave={onSave}
          onTitleChange={undefined}
          onDraftChange={setOutlineMd}
        />
        <div className="work-foot">
          <div className="doc-foot-row">
            <Text type="tertiary" size="small">
              {work ? `归属 ${work.title}` : '资料'} · v{doc.contentVer}
            </Text>
            {linkedTitles.length > 0 && (
              <span className="doc-links">
                <Text type="tertiary" size="small">
                  关联 Wiki：
                </Text>
                {linkedTitles.map((l) => (
                  <Button
                    key={l.id}
                    size="small"
                    theme="borderless"
                    type="tertiary"
                    onClick={() => nav(workPath(l.id))}
                  >
                    {l.title}
                  </Button>
                ))}
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
