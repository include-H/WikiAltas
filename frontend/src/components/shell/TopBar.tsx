import { useMemo } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { Avatar, Breadcrumb, Button, Dropdown, Tooltip } from '@douyinfe/semi-ui'
import {
  IconAIFilledLevel1,
  IconChevronRight,
  IconEdit,
  IconHistory,
  IconMore,
  IconSun,
  IconMoon,
} from '@douyinfe/semi-icons'
import { useAppStore, type DocMode } from '../../lib/store'
import { UNKNOWN_WORK_ID, workPath } from '../../lib/routes'
import { ancestorPath, relativeTime } from '../../lib/tree'
import { toggleTheme } from '../../lib/theme'

const ROOT_CRUMB = { name: '我的文档库', path: '/' }

export default function TopBar() {
  const nav = useNavigate()
  const loc = useLocation()
  const params = useParams()
  const { nodes, setAiPanelOpen, docMode, setDocMode } = useAppStore()

  const routeId = params.id
  const workId = routeId && routeId !== UNKNOWN_WORK_ID ? routeId : null
  const current = useMemo(() => nodes.find((n) => n.id === workId), [nodes, workId])

  const path = useMemo(
    () => (workId ? ancestorPath(nodes, workId) : []),
    [nodes, workId],
  )

  const onDoc = loc.pathname.startsWith('/w/')
  const onSettings = loc.pathname === '/settings'
  const onRuns = loc.pathname === '/runs'

  const routes = useMemo(() => {
    if (onSettings) return [ROOT_CRUMB, { name: '设置' }]
    if (onRuns) return [ROOT_CRUMB, { name: '馆员工单' }]
    if (!onDoc || path.length === 0) return [ROOT_CRUMB, { name: '首页' }]
    const crumbs: { name: string; path?: string }[] = [
      ROOT_CRUMB,
      ...path.map((n) => ({ name: n.title, path: workPath(n.id, n.slug) })),
    ]
    // 资料夹模式：面包屑补一级「资料夹」（左栏此时是资料列表）
    if (/\/folder(\/|$)/.test(loc.pathname)) {
      crumbs.push({ name: '资料夹' })
    }
    return crumbs
  }, [loc.pathname, onDoc, onRuns, onSettings, path])

  const updatedLabel = current ? relativeTime(current.updatedAt) : ''

  const MODE_LABEL: Record<DocMode, string> = {
    edit: '编辑',
    revision: '修订',
    read: '只读',
  }

  return (
    <header className="top-bar">
      <div className="top-bar-crumbs">
        <Breadcrumb
          compact
          routes={routes}
          separator={<IconChevronRight size="extra-small" />}
          onClick={(route) => {
            if (route.path) nav(String(route.path))
          }}
        />
        {updatedLabel && <span className="top-bar-updated">最近修改：{updatedLabel}</span>}
      </div>

      <div className="top-bar-actions">
        {/* 对齐飞书：模式下拉（编辑 / 修订 / 只读）+ AI 入口（问豆包位）+ 更多 + 头像 */}
        {onDoc && (
          <Dropdown
            trigger="click"
            position="bottomRight"
            render={
              <Dropdown.Menu>
                <Dropdown.Item onClick={() => setDocMode('edit')}>编辑</Dropdown.Item>
                <Dropdown.Item onClick={() => setDocMode('revision')}>修订</Dropdown.Item>
                <Dropdown.Item onClick={() => setDocMode('read')}>只读</Dropdown.Item>
              </Dropdown.Menu>
            }
          >
            <Button
              theme="borderless"
              type="tertiary"
              size="small"
              icon={<IconEdit />}
            >
              {MODE_LABEL[docMode]}
            </Button>
          </Dropdown>
        )}
        <Tooltip content="问馆员" position="bottom">
          <Button
            className="ai-topbar-btn"
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconAIFilledLevel1 />}
            onClick={() => setAiPanelOpen(true)}
          >
            问馆员
          </Button>
        </Tooltip>
        <Dropdown
          trigger="click"
          position="bottomRight"
          render={
            <Dropdown.Menu>
              <Dropdown.Item onClick={() => nav('/runs')}>馆员工单</Dropdown.Item>
              <Dropdown.Item onClick={() => nav('/settings')}>设置</Dropdown.Item>
              <Dropdown.Item
                icon={<IconSun size="small" />}
                onClick={() => {
                  if (document.body.getAttribute('theme-mode') !== 'dark') toggleTheme()
                }}
              >
                浅色模式
              </Dropdown.Item>
              <Dropdown.Item
                icon={<IconMoon size="small" />}
                onClick={() => {
                  if (document.body.getAttribute('theme-mode') !== 'light') toggleTheme()
                }}
              >
                深色模式
              </Dropdown.Item>
              <Dropdown.Divider />
              <Dropdown.Item icon={<IconHistory size="small" />} onClick={() => nav('/runs')}>
                最近工单
              </Dropdown.Item>
            </Dropdown.Menu>
          }
        >
          <Button
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconMore />}
            aria-label="更多"
          />
        </Dropdown>
        <Avatar size="extra-small" color="light-blue">
          我
        </Avatar>
      </div>
    </header>
  )
}
