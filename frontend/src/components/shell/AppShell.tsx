import { Outlet, useParams } from 'react-router-dom'
import { FloatButton, Layout } from '@douyinfe/semi-ui'
import { IconAIFilledLevel1 } from '@douyinfe/semi-icons'
import { useEffect, useMemo } from 'react'
import { useLocation } from 'react-router-dom'
import SideNav from './SideNav'
import TopBar from './TopBar'
import AiPanel from './AiPanel'
import { useAppStore } from '../../lib/store'
import { UNKNOWN_WORK_ID } from '../../lib/routes'

export default function AppShell() {
  const { aiPanelOpen, setAiPanelOpen, nodes, sidebarCollapsed, activeRuns, setActiveBatchId } =
    useAppStore()
  const params = useParams()
  const loc = useLocation()

  // 深链：?ai=1 打开馆员面板；?batch=<id> 打开批次卡（两者都能用于外部链接）
  useEffect(() => {
    const params = new URLSearchParams(loc.search)
    if (params.get('ai') === '1') setAiPanelOpen(true)
    const batch = params.get('batch')
    if (batch) setActiveBatchId(batch)
  }, [loc.search, setAiPanelOpen, setActiveBatchId])

  // UUID-first: :id 是身份；哨兵 `-` 表示只有资料 id 的深链
  const routeId = params.id
  const workId = useMemo(() => {
    if (!routeId || routeId === UNKNOWN_WORK_ID) return null
    return routeId
  }, [routeId])
  const workTitle = useMemo(
    () => nodes.find((n) => n.id === workId)?.title,
    [nodes, workId],
  )
  // 打开资料时把资料 id 一并给面板：意图才会是 write_doc，而不是拿系列去 continue_wiki
  const docId = params.docId ?? null

  return (
    <Layout hasSider className="app-shell">
      <Layout.Sider
        className={`shell-side${sidebarCollapsed ? ' is-collapsed' : ''}`}
        style={{ width: sidebarCollapsed ? 48 : 280 }}
      >
        <SideNav />
      </Layout.Sider>
      <Layout.Content className="shell-main">
        <TopBar />
        <div className="shell-body">
          <Outlet />
        </div>
        {aiPanelOpen ? (
          <div className="ai-float">
            <AiPanel workId={workId} workTitle={workTitle} docId={docId} />
          </div>
        ) : (
          <FloatButton
            className="ai-float-btn"
            shape="round"
            size="large"
            icon={<IconAIFilledLevel1 />}
            badge={activeRuns.length > 0 ? { count: activeRuns.length } : undefined}
            onClick={() => setAiPanelOpen(true)}
          />
        )}
      </Layout.Content>
    </Layout>
  )
}
