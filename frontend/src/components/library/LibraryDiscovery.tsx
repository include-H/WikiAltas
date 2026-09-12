import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import type { NodeSweepResponse, NodeSweepSourceStatus, NodeSweepSuggestion, Work } from '../../types'
import {
  dismissNodeSweep,
  getNodeSweep,
  linkLibrary,
  restoreNodeSweep,
  runNodeSweep,
} from '../../lib/api'
import type { LibraryApiSource } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { FORMAT_LABEL, KIND_LABEL, SOURCE_LABEL, confColor } from '../../lib/mediaTags'

const { Text } = Typography

/**
 * 节点页的「媒体库发现」（节点 → 库）：
 * 写完之后宿主自动扫库（这里轮询等它出结果），也可手动「检索媒体库」重扫；
 * 三源里属于这部作品的正片/改编影视/解析攻略视频/漫画小说版列成待确认建议，
 * 点「关联」才挂链。忽略是 per-node 的（不再向这个节点提起），与池子的全局忽略互不影响。
 */
export default function LibraryDiscovery({
  work,
  onLinked,
}: {
  work: Work
  onLinked?: () => void
}) {
  const { me, refreshTree, lastCommitted } = useAppStore()
  const [sugs, setSugs] = useState<NodeSweepSuggestion[]>([])
  const [dismissed, setDismissed] = useState<NodeSweepResponse['dismissed']>([])
  const [sources, setSources] = useState<NodeSweepSourceStatus[]>([])
  const [scannedOnce, setScannedOnce] = useState(false)
  const [scanning, setScanning] = useState(false)
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [showDismissed, setShowDismissed] = useState(false)

  // 请求序号：晚到的旧响应不许盖掉新状态（切换作品 / 轮询 / 手动重扫会叠请求）。
  // 手动重扫的响应不含 dismissed（忽略记录只在 GET 里读），别把它清空。
  const seqRef = useRef(0)
  const apply = useCallback(
    (res: {
      suggestions?: NodeSweepResponse['suggestions']
      sources?: NodeSweepSourceStatus[]
      dismissed?: NodeSweepResponse['dismissed']
    }) => {
      setSugs(res.suggestions ?? [])
      if (res.dismissed) setDismissed(res.dismissed)
      if (res.sources) setSources(res.sources)
      setScannedOnce(true)
    },
    [],
  )
  const load = useCallback(async () => {
    const seq = ++seqRef.current
    try {
      const res = await getNodeSweep(work.id)
      if (seq !== seqRef.current) return
      apply(res)
    } catch {
      if (seq !== seqRef.current) return
    }
  }, [work.id, apply])

  useEffect(() => {
    setSugs([])
    setDismissed([])
    setSources([])
    setScannedOnce(false)
    if (!me.authed) return
    void load()
  }, [load, me.authed])

  // 写完之后：宿主在后台扫库（一次判官要十几秒），轻量轮询直到结果出现。
  const [watching, setWatching] = useState(false)
  useEffect(() => {
    if (!me.authed || !lastCommitted || lastCommitted.targetId !== work.id) return
    setWatching(true)
    const done = setTimeout(() => setWatching(false), 90_000)
    return () => clearTimeout(done)
  }, [lastCommitted, work.id, me.authed])
  useEffect(() => {
    if (!watching) return
    const iv = setInterval(() => {
      void getNodeSweep(work.id)
        .then((res) => {
          if (res.suggestions?.length) {
            apply(res)
            setWatching(false)
          }
        })
        .catch(() => {})
    }, 5000)
    return () => clearInterval(iv)
  }, [watching, work.id, apply])

  const rescan = useCallback(async () => {
    setScanning(true)
    try {
      const res = await runNodeSweep(work.id)
      apply(res)
      if (!res.suggestions?.length) {
        Toast.info('兜了一圈，没找到属于它的条目')
      }
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '检索失败')
    } finally {
      setScanning(false)
    }
  }, [work.id, apply])

  const link = useCallback(
    async (s: NodeSweepSuggestion) => {
      setBusyKey(s.key)
      try {
        await linkLibrary(s.source as LibraryApiSource, work.id, s.entryId)
        Toast.success(`已关联「${s.title}」`)
        setSugs((prev) => prev.filter((it) => it.key !== s.key))
        void refreshTree()
        onLinked?.()
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : '关联失败')
      } finally {
        setBusyKey(null)
      }
    },
    [work.id, refreshTree, onLinked],
  )

  const linkAllConfident = useCallback(async () => {
    const list = sugs.filter((s) => s.confidence >= 0.8)
    if (!list.length) return
    let ok = 0
    for (const s of list) {
      try {
        await linkLibrary(s.source as LibraryApiSource, work.id, s.entryId)
        ok++
        setSugs((prev) => prev.filter((it) => it.key !== s.key))
      } catch {
        /* 单条失败继续 */
      }
    }
    Toast.success(`已关联 ${ok} 条`)
    void refreshTree()
    onLinked?.()
  }, [sugs, work.id, refreshTree, onLinked])

  const dismiss = useCallback(
    async (s: NodeSweepSuggestion) => {
      setBusyKey(s.key)
      try {
        await dismissNodeSweep(work.id, s.key)
        setSugs((prev) => prev.filter((it) => it.key !== s.key))
        setDismissed((prev) => [
          ...prev,
          { workId: work.id, key: s.key, source: s.source, title: s.title },
        ])
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : '忽略失败')
      } finally {
        setBusyKey(null)
      }
    },
    [work.id],
  )

  const restore = useCallback(
    async (key: string) => {
      setBusyKey(key)
      try {
        await restoreNodeSweep(work.id, key)
        setDismissed((prev) => prev.filter((d) => d.key !== key))
        // 恢复后直接重扫一次，让条目回到候选里
        await rescan()
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : '恢复失败')
      } finally {
        setBusyKey(null)
      }
    },
    [work.id, rescan],
  )

  if (!me.authed) return null

  const unconfigured = sources.filter((s) => s.status !== 'ok' && s.message)
  const empty = sugs.length === 0 && dismissed.length === 0

  // 没结果也没忽略记录：留一行细条，保证「检索媒体库」这个动作找得到
  if (empty && !scanning && !watching) {
    return (
      <div className="ga-suggest sweep-slim">
        <div className="ga-suggest-head" style={{ marginBottom: 0 }}>
          <Text strong>媒体库发现</Text>
          <Text type="tertiary" size="small">
            {scannedOnce ? '没找到属于它的条目' : `从媒体库找「${work.title}」的正片 / 改编影视 / 解析攻略`}
          </Text>
          <Button size="small" loading={scanning} onClick={() => void rescan()}>
            {scannedOnce ? '重新检索' : '检索媒体库'}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="ga-suggest">
      <div className="ga-suggest-head">
        <Text strong>媒体库发现</Text>
        <Text type="tertiary" size="small">
          {scanning
            ? '正在检索媒体库…'
            : watching
              ? '写完啦，正在媒体库里找它的条目…'
              : `「${work.title}」在媒体库里的条目（确认后才关联）`}
        </Text>
        <Button size="small" loading={scanning} onClick={() => void rescan()}>
          {scanning ? '检索中' : '重新检索'}
        </Button>
        {sugs.filter((s) => s.confidence >= 0.8).length > 1 && (
          <Button size="small" theme="solid" type="primary" onClick={() => void linkAllConfident()}>
            关联全部高置信
          </Button>
        )}
      </div>
      {unconfigured.length > 0 && (
        <div className="sweep-hints">
          {unconfigured.map((s) => (
            <Text key={s.source} type="warning" size="small">
              {SOURCE_LABEL[s.source] ?? s.source}：{s.message}
            </Text>
          ))}
        </div>
      )}
      {sugs.length > 0 && (
        <div className="ga-suggest-list">
          {sugs.map((s) => (
            <div className="ga-suggest-row" key={s.key}>
              <span className="ga-suggest-cover">
                {s.coverImage ? <img src={s.coverImage} alt="" loading="lazy" /> : null}
              </span>
              <span className="ga-suggest-meta">
                <span className="ga-suggest-title" title={s.title}>
                  {s.title}
                </span>
                <Text type="tertiary" size="small">
                  <Tag size="small" color={confColor(s.confidence)} style={{ marginRight: 6 }}>
                    {Math.round(s.confidence * 100)}%
                  </Tag>
                  <Tag size="small" style={{ marginRight: 6 }}>
                    {SOURCE_LABEL[s.source] ?? s.source}
                  </Tag>
                  {s.kind ? (
                    <Tag size="small" color="blue" style={{ marginRight: 6 }}>
                      {KIND_LABEL[s.kind] ?? s.kind}
                    </Tag>
                  ) : null}
                  {s.format ? (
                    <Tag size="small" color="cyan" style={{ marginRight: 6 }}>
                      {FORMAT_LABEL[s.format] ?? s.format}
                    </Tag>
                  ) : null}
                  {s.reason}
                  {s.extra ? ` · ${s.extra}` : ''}
                </Text>
              </span>
              <a className="ga-suggest-link" href={s.url} target="_blank" rel="noreferrer">
                打开
              </a>
              <Button
                size="small"
                theme="solid"
                type="primary"
                loading={busyKey === s.key}
                onClick={() => void link(s)}
              >
                关联
              </Button>
              <Button
                size="small"
                type="tertiary"
                disabled={busyKey === s.key}
                onClick={() => void dismiss(s)}
              >
                忽略
              </Button>
            </div>
          ))}
        </div>
      )}
      {dismissed.length > 0 && (
        <div className="sweep-dismissed">
          <a className="sweep-toggle" onClick={() => setShowDismissed((v) => !v)}>
            已忽略 {dismissed.length} 条{showDismissed ? '（收起）' : ''}
          </a>
          {showDismissed &&
            dismissed.map((d) => (
              <span className="sweep-dismissed-row" key={d.key}>
                <Text type="tertiary" size="small">
                  {d.title || d.key}
                </Text>
                <a className="sweep-toggle" onClick={() => void restore(d.key)}>
                  {busyKey === d.key ? '恢复中…' : '恢复'}
                </a>
              </span>
            ))}
        </div>
      )}
    </div>
  )
}
