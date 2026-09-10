import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { Banner, Button, Empty, Spin, Toast, Typography } from '@douyinfe/semi-ui'
import type { Work } from '../types'
import { getWork, putWorkContent, patchWork } from '../lib/api'
import { useAppStore } from '../lib/store'
import { workPath } from '../lib/routes'
import OutlinePane from '../components/shell/OutlinePane'
import MarkdownEditor from '../components/editor/MarkdownEditor'
import DocActions from '../components/editor/DocActions'
import { parseOutline } from '../lib/mdOutline'

const { Text } = Typography

export default function WorkView() {
  const { id } = useParams()
  const nav = useNavigate()
  const loc = useLocation()
  const { contentStamp, lastCommitted, staging, setAiPanelOpen, docMode } = useAppStore()
  const [work, setWork] = useState<Work | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [outlineMd, setOutlineMd] = useState('')
  // 切换作品时的竞态保护：慢请求回来时如果路由已经变了，就丢弃结果
  const routeIdRef = useRef<string | undefined>(id)
  routeIdRef.current = id

  const load = useCallback(async () => {
    if (!id) return
    const requested = id
    setLoading(true)
    setError(null)
    try {
      // Resolve solely by UUID — slug in the URL is cosmetic and ignored
      const res = await getWork(requested)
      if (routeIdRef.current !== requested) return
      if (!res?.work) throw new Error('作品不存在或已被删除')
      setWork(res.work)
      setOutlineMd(res.work.contentMd ?? '')
    } catch (e) {
      if (routeIdRef.current !== requested) return
      setError(e instanceof Error ? e.message : '加载作品失败')
      setWork(null)
    } finally {
      if (routeIdRef.current === requested) setLoading(false)
    }
  }, [id])

  useEffect(() => {
    void load()
  }, [load])

  // If a cosmetic slug is present but stale, replaceState to the canonical URL.
  // Bare `/w/:id` is valid and is NOT rewritten to include a slug.
  useEffect(() => {
    if (!work || !id) return
    // 关键：切换文章时 work 还停留在上一篇，这时绝不能拿它去改写 URL，
    // 否则会出现"点第二篇又跳回第一篇"的来回切换。
    if (work.id !== id) return
    const rest = loc.pathname.slice(`/w/${id}`.length)
    if (!rest.startsWith('/') || rest === '/') return
    const seg = decodeURIComponent(rest.slice(1).split('/')[0] ?? '')
    if (seg === '' || seg === 'folder') return
    if (seg !== work.slug) {
      nav(workPath(work.id, work.slug), { replace: true })
    }
  }, [work, id, loc.pathname, nav])

  // refresh when AI commits to this work
  useEffect(() => {
    if (!work || !lastCommitted) return
    if (lastCommitted.targetId === work.id) {
      void load()
    }
  }, [contentStamp, lastCommitted, work, load])

  const headings = useMemo(() => parseOutline(outlineMd), [outlineMd])

  // "馆员正在写入"只在 content.staging 期间成立；提交后由 content.committed 清掉
  const aiWriting = useMemo(
    () => !!work && staging?.targetId === work.id,
    [staging, work],
  )

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
        <MarkdownEditor
          key={work.id}
          title={work.title}
          updatedAt={work.updatedAt}
          contentMd={work.contentMd ?? ''}
          contentVer={work.contentVer}
          saving={saving}
          aiWriting={aiWriting}
          mode={docMode === 'read' ? 'read' : 'edit'}
          actions={
            <DocActions
              workId={work.id}
              workTitle={work.title}
              onReload={load}
              onDeleted={() => nav('/')}
            />
          }
          onAiWrite={() => setAiPanelOpen(true)}
          onSave={onSave}
          onTitleChange={onTitleChange}
          onDraftChange={setOutlineMd}
        />
        <div className="work-foot">
          <Text type="tertiary" size="small">
            id: {work.id} · slug: {work.slug} · kind: {work.kind}
            {work.medium ? ` · ${work.medium}` : ''} · status: {work.status}
          </Text>
        </div>
      </div>
    </div>
  )
}
