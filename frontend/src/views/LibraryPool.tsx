import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Banner, Button, Empty, Modal, Spin, Toast, Typography } from '@douyinfe/semi-ui'
import { IconAIFilledLevel1 } from '@douyinfe/semi-icons'
import type { LibraryPoolResponse, PoolAiSuggestion, PoolEntry, PoolGroup } from '../types'
import {
  aiSuggestLibrary,
  archiveLibrary,
  createBatchWiki,
  getLibraryPool,
  ignoreLibrary,
  linkLibrary,
  scanLibrary,
  unignoreLibrary,
} from '../lib/api'
import type { LibraryApiSource } from '../lib/api'
import { useAppStore } from '../lib/store'
import { buildTreeData, relativeTime } from '../lib/tree'
import { SOURCE_ORDER } from '../lib/mediaTags'
import PoolAiCard from '../components/library/PoolAiCard'
import PoolIgnoredCard from '../components/library/PoolIgnoredCard'
import PoolSources from '../components/library/PoolSources'
import PoolStubCard from '../components/library/PoolStubCard'
import { PoolBatchModal, PoolPickModal } from '../components/library/PoolModals'

const { Text, Title } = Typography

/**
 * 媒体库建议（建议池）——库状态仪表盘：
 * 数据来自后台低频扫描的清单快照（秒开）；挂链状态实时叠加。
 * 动作只有三件、全是人点的：关联到已有节点 / 建档（挂到所选父节点）/ 忽略（单条或整组）；
 * 「AI 对一遍」只产出建议（带可信度），点确认才执行——LLM 绝不主动建库。
 */
export default function LibraryPool() {
  const { me, nodes, refreshTree, setActiveBatchId, setAiPanelOpen } = useAppStore()
  const [data, setData] = useState<LibraryPoolResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [scanning, setScanning] = useState(false)
  const [aiRunning, setAiRunning] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [pick, setPick] = useState<{ entry: PoolEntry; mode: 'link' | 'archive' } | null>(null)
  const [pickParent, setPickParent] = useState<string | undefined>(undefined)
  const [onlyNew, setOnlyNew] = useState(false)
  // 来源分栏：平级页签，点击切换（GameAtlas / Emby / Komga）
  const [activeSource, setActiveSource] = useState<string>('gameatlas')
  const didPickTab = useRef(false)
  const [batchOpen, setBatchOpen] = useState(false)
  const [batchSize, setBatchSize] = useState('5')
  const [batchStarting, setBatchStarting] = useState(false)

  const stubs = useMemo(() => nodes.filter((n) => n.status === 'stub'), [nodes])
  const treeData = useMemo(() => buildTreeData(nodes), [nodes])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setData(await getLibraryPool())
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '读取建议池失败')
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (me.authed) void load()
  }, [me.authed, load])

  // 首次拿到数据时挑一次页签：默认 GameAtlas，若它没内容就落到第一个有内容的来源
  useEffect(() => {
    if (!data || didPickTab.current) return
    const groupsOf = (src: string) => data.groups.filter((g) => g.source === src && g.unlinked.length > 0)
    if (groupsOf(activeSource).length === 0) {
      const first = SOURCE_ORDER.find((src) => groupsOf(src).length > 0)
      if (first && first !== activeSource) setActiveSource(first)
    }
    didPickTab.current = true
  }, [data, activeSource])

  const itemKey = (e: PoolEntry) => `${e.source}:${e.publicId}`

  const runScan = async () => {
    setScanning(true)
    try {
      const res = await scanLibrary()
      Toast.success(`扫描完成：共 ${res.entries} 条（未挂链 ${res.unlinked}）`)
      await load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '扫描失败')
    } finally {
      setScanning(false)
    }
  }

  const runAi = async () => {
    setAiRunning(true)
    try {
      const res = await aiSuggestLibrary()
      Toast.success(
        `AI 对完一遍：关联建议 ${res.links} · 建档建议 ${res.archives} · 建议忽略 ${res.ignored}（${res.model}）`,
      )
      await load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : 'AI 匹配失败')
    } finally {
      setAiRunning(false)
    }
  }

  const doArchive = async (entry: PoolEntry, parentId: string) => {
    setBusy(itemKey(entry))
    try {
      const res = await archiveLibrary(entry.source as LibraryApiSource, parentId, entry.publicId)
      Toast.success(`已建档「${res.work?.title ?? entry.title}」`)
      await load()
      await refreshTree()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '建档失败')
    } finally {
      setBusy(null)
    }
  }

  const doLink = async (entry: PoolEntry, nodeId: string) => {
    setBusy(itemKey(entry))
    try {
      await linkLibrary(entry.source as LibraryApiSource, nodeId, entry.publicId)
      Toast.success(`已关联「${entry.title}」`)
      await load()
      await refreshTree()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '关联失败')
    } finally {
      setBusy(null)
    }
  }

  const ignoreEntry = async (entry: PoolEntry) => {
    try {
      await ignoreLibrary({ source: entry.source, entryId: entry.publicId })
      Toast.success(`已忽略「${entry.title}」`)
      await load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '忽略失败')
    }
  }

  const ignoreGroup = (group: PoolGroup) => {
    Modal.confirm({
      title: `忽略整组「${group.containerTitle}」？`,
      content: '这一组里的条目将不再出现在建议池（可在「已忽略」里恢复）。',
      okText: '忽略整组',
      onOk: async () => {
        try {
          await ignoreLibrary({
            source: group.source,
            containerKey: group.containerKey,
            containerTitle: group.containerTitle,
          })
          Toast.success('已忽略整组')
          await load()
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '忽略失败')
        }
      },
    })
  }

  const restoreEntry = async (entry: PoolEntry) => {
    try {
      await unignoreLibrary({ source: entry.source, entryId: entry.publicId })
      await load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '恢复失败')
    }
  }

  const restoreGroup = async (source: string, containerKey: string) => {
    try {
      await unignoreLibrary({ source, containerKey })
      await load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '恢复失败')
    }
  }

  const execSuggestion = async (sug: PoolAiSuggestion) => {
    if (!sug.targetNodeId && sug.action !== 'ignore') return
    setBusy(itemKey(sug.entry))
    try {
      if (sug.action === 'link') await doLink(sug.entry, sug.targetNodeId as string)
      else if (sug.action === 'archive') await doArchive(sug.entry, sug.targetNodeId as string)
      else await ignoreEntry(sug.entry)
    } finally {
      setBusy(null)
    }
  }

  const startBatch = async () => {
    setBatchStarting(true)
    try {
      const res = await createBatchWiki({
        workIds: stubs.map((s) => s.id),
        batchSize: Number(batchSize),
      })
      setActiveBatchId(res.batchId)
      setAiPanelOpen(true)
      setBatchOpen(false)
      Toast.success(`已排入 ${res.runIds.length} 个建档工单`)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '创建批次失败')
    } finally {
      setBatchStarting(false)
    }
  }

  if (!me.authed) {
    return <Empty description="建议池需要登录后查看" style={{ margin: 64 }} />
  }

  const openPick = (entry: PoolEntry, mode: 'link' | 'archive') => {
    setPick({ entry, mode })
    setPickParent(undefined)
  }

  const allEmpty =
    !!data &&
    data.groups.every((g) => g.unlinked.length === 0) &&
    data.aiSuggestions.length === 0 &&
    data.ignoredEntries.length === 0

  return (
    <div className="pool-page">
      <div className="pool-inner">
        <div className="pool-head">
          <Title heading={4} style={{ margin: 0 }}>
            媒体库建议
          </Title>
          <div className="pool-head-btns">
            <Button loading={scanning} onClick={() => void runScan()}>
              扫描媒体库
            </Button>
            <Button
              theme="solid"
              type="primary"
              icon={<IconAIFilledLevel1 />}
              className="ai-coedit-btn"
              loading={aiRunning}
              disabled={!data?.scannedAt}
              onClick={() => void runAi()}
            >
              AI 对一遍
            </Button>
          </div>
        </div>
        <Text type="tertiary" size="small">
          {data?.scannedAt
            ? `扫描于 ${relativeTime(data.scannedAt)}（每 ${data.scanIntervalMinutes} 分钟自动扫一次，可在设置里改）`
            : '还没扫描过——点「扫描媒体库」把 Emby / Komga / GameAtlas 的清单拉成快照。'}
          {data?.aiGeneratedAt
            ? ` · AI 建议于 ${relativeTime(data.aiGeneratedAt)}（${data.aiModel}）`
            : ''}
        </Text>

        <PoolStubCard stubs={stubs} nodes={nodes} onBatch={() => setBatchOpen(true)} />

        {loading && !data ? (
          <Spin style={{ display: 'block', margin: '48px auto' }} />
        ) : !data ? (
          <Empty style={{ margin: 48 }} description="读取失败" />
        ) : !data.scannedAt ? (
          <Banner
            type="info"
            closeIcon={null}
            style={{ marginTop: 12 }}
            description="扫描后：这里按「源 → 库/系列」列出还没挂链的条目，可关联、建档或忽略；「AI 对一遍」会给带可信度的匹配建议。"
          />
        ) : (
          <>
            {data.sourcesConfigured?.emby && !data.rolesConfigured && (
              <Banner
                type="info"
                closeIcon={null}
                style={{ marginTop: 12 }}
                description="还没给 Emby 媒体库标角色——去设置页把「正片 / 混杂内容」点出来，扫描才会收录它们。"
              />
            )}

            <PoolAiCard
              suggestions={data.aiSuggestions}
              busyKey={busy}
              keyOf={itemKey}
              onConfirm={(sug) => void execSuggestion(sug)}
            />

            {allEmpty ? (
              <Empty style={{ margin: 48 }} description="没有待处理的库条目" />
            ) : (
              <PoolSources
                groups={data.groups}
                onlyNew={onlyNew}
                onOnlyNewChange={setOnlyNew}
                activeSource={activeSource}
                onActiveSourceChange={setActiveSource}
                busyKey={busy}
                keyOf={itemKey}
                onArchive={(e) => openPick(e, 'archive')}
                onLink={(e) => openPick(e, 'link')}
                onIgnoreEntry={(e) => void ignoreEntry(e)}
                onIgnoreGroup={ignoreGroup}
              />
            )}

            <PoolIgnoredCard
              entries={data.ignoredEntries}
              groups={data.ignoredGroups}
              onRestoreEntry={(e) => void restoreEntry(e)}
              onRestoreGroup={(source, key) => void restoreGroup(source, key)}
            />
          </>
        )}

        <PoolPickModal
          pick={pick}
          treeData={treeData}
          pickParent={pickParent}
          onPickParentChange={setPickParent}
          onCancel={() => setPick(null)}
          onOk={() => {
            if (!pick || !pickParent) return
            const { entry, mode } = pick
            setPick(null)
            if (mode === 'archive') void doArchive(entry, pickParent)
            else void doLink(entry, pickParent)
          }}
        />
        <PoolBatchModal
          open={batchOpen}
          stubCount={stubs.length}
          batchSize={batchSize}
          onBatchSizeChange={setBatchSize}
          starting={batchStarting}
          onCancel={() => setBatchOpen(false)}
          onOk={() => void startBatch()}
        />
      </div>
    </div>
  )
}
