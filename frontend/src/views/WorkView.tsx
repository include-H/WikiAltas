import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Banner, Button, Empty, Spin, Tag, Toast } from '@douyinfe/semi-ui'
import type { LibraryLink, Work } from '../types'
import { getWork, putWorkContent, patchWork } from '../lib/api'
import { useAppStore } from '../lib/store'
import OutlinePane from '../components/shell/OutlinePane'
import MarkdownEditor from '../components/editor/MarkdownEditor'
import DocActions from '../components/editor/DocActions'
import PendingRevisions from '../components/editor/PendingRevisions'
import WorkSidePanel from '../components/editor/WorkSidePanel'
import { parseOutline } from '../lib/mdOutline'
import { useLiveRefresh } from '../lib/useLiveRefresh'

export default function WorkView() {
  const { id } = useParams()
  const nav = useNavigate()
  const { contentStamp, lastCommitted, staging, setAiPanelOpen, docMode, me } = useAppStore()
  const [work, setWork] = useState<Work | null>(null)
  const [links, setLinks] = useState<LibraryLink[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [outlineMd, setOutlineMd] = useState('')
  // 切换作品时的竞态保护：慢请求回来时如果路由已经变了，就丢弃结果
  const routeIdRef = useRef<string | undefined>(id)
  routeIdRef.current = id

  const load = useCallback(async (opts?: { silent?: boolean }) => {
    if (!id) return
    const requested = id
    if (!opts?.silent) {
      setLoading(true)
      setError(null)
    }
    try {
      // Resolve solely by UUID — 路径里除了 UUID 不认别的（旧链接多带的 slug 段忽略）
      const res = await getWork(requested)
      if (routeIdRef.current !== requested) return
      if (!res?.work) throw new Error('作品不存在或已被删除')
      setWork(res.work)
      setLinks(res.libraryLinks ?? [])
      setOutlineMd(res.work.contentMd ?? '')
    } catch (e) {
      if (routeIdRef.current !== requested) return
      if (opts?.silent) return
      setError(e instanceof Error ? e.message : '加载作品失败')
      setWork(null)
    } finally {
      if (!opts?.silent && routeIdRef.current === requested) setLoading(false)
    }
  }, [id])

  useEffect(() => {
    void load()
  }, [load])

  // refresh when AI commits to this work
  useEffect(() => {
    if (!work || !lastCommitted) return
    if (lastCommitted.targetId === work.id) {
      void load()
    }
  }, [contentStamp, lastCommitted, work, load])

  const headings = useMemo(() => parseOutline(outlineMd), [outlineMd])

  // "Altas 正在写入"只在 wikiatlas.content.staging 期间成立；提交后由 wikiatlas.content.committed 清掉
  const aiWriting = useMemo(
    () => !!work && staging?.targetId === work.id,
    [staging, work],
  )

  // Altas 正在写：每 2 秒静默拉一次正文，用户能看着它一行行长出来
  useLiveRefresh(aiWriting, useCallback(() => load({ silent: true }), [load]))

  const onSave = useCallback(
    async (body: Parameters<typeof putWorkContent>[1]) => {
      if (!work) throw new Error('作品未加载')
      setSaving(true)
      try {
        const res = await putWorkContent(work.id, body)
        setWork((w) =>
          w
            ? { ...w, contentMd: body.contentMd, contentVer: res.contentVer, updatedAt: new Date().toISOString() }
            : w,
        )
        setOutlineMd(body.contentMd)
        return res
      } finally {
        setSaving(false)
      }
    },
    [work],
  )

  const onTitleChange = useCallback(
    async (title: string) => {
      if (!work) return
      try {
        const w = await patchWork(work.id, { title })
        setWork(w)
        Toast.success('标题已更新')
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : '标题更新失败')
      }
    },
    [work],
  )

  if (loading && !work) {
    return <Spin style={{ display: 'block', margin: '64px auto' }} />
  }

  if (error && !work) {
    return (
      <div style={{ padding: 32 }}>
        <Banner type="danger" description={error} />
        <div style={{ marginTop: 16 }}>
          <Button onClick={() => void load()}>重试</Button>
          <Button style={{ marginLeft: 8 }} onClick={() => nav('/')}>
            回首页
          </Button>
        </div>
      </div>
    )
  }

  if (!work) {
    return <Empty description="作品不存在" style={{ margin: 64 }} />
  }

  return (
    <div className="work-view">
      <OutlinePane
        headings={headings}
        title={work.title}
        contentKey={`${work.contentVer}:${outlineMd.length}`}
      />
      <div className="work-content">
        {me.authed && <PendingRevisions workId={work.id} onReload={load} />}
        <MarkdownEditor
          key={work.id}
          title={work.title}
          updatedAt={work.updatedAt}
          contentMd={work.contentMd ?? ''}
          contentVer={work.contentVer}
          saving={saving}
          aiWriting={aiWriting}
          mode={!me.authed || docMode === 'read' ? 'read' : 'edit'}
          actions={me.authed ? (
            <DocActions
              work={work}
              onReload={load}
              onDeleted={() => nav('/')}
              libraryLinks={links}
            />
          ) : (
            <Tag size="small" color={work.visibility === 'public' ? 'green' : 'grey'}>
              {work.visibility === 'public' ? '公开' : '私有'}
            </Tag>
          )}
          onAiWrite={() => setAiPanelOpen(true)}
          onSave={onSave}
          onTitleChange={me.authed ? onTitleChange : undefined}
          onDraftChange={setOutlineMd}
        />
      </div>
      {me.authed && (
        <WorkSidePanel work={work} links={links} onLinked={() => void load({ silent: true })} />
      )}
    </div>
  )
}
