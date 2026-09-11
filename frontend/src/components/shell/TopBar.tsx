import { useMemo } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { Avatar, Breadcrumb, Button, Dropdown, Toast, Tooltip } from '@douyinfe/semi-ui'
import {
  IconAIFilledLevel1,
  IconChevronRight,
  IconEdit,
  IconGlobe,
  IconHistory,
  IconLock,
  IconMore,
  IconSun,
  IconMoon,
} from '@douyinfe/semi-icons'
import { useAppStore, type DocMode } from '../../lib/store'
import { UNKNOWN_WORK_ID, workPath } from '../../lib/routes'
import { patchWork } from '../../lib/api'
import { ancestorPath, relativeTime } from '../../lib/tree'
import { toggleTheme } from '../../lib/theme'

const ROOT_CRUMB = { name: '我的文档库', path: '/' }

export default function TopBar() {
  const nav = useNavigate()
  const loc = useLocation()
  const params = useParams()
  const { nodes, setAiPanelOpen, docMode, setDocMode, me, refreshMe, refreshTree } = useAppStore()
  const nav2 = useNavigate()

  const doLogout = async () => {
    const { logout } = await import('../../lib/api')
    await logout().catch(() => undefined)
    await refreshMe()
    nav2('/')
  }

  /** 可见性开关（对齐飞书顶栏的锁图标）：私有 = 仅登录可见，公开 = 访客可读。 */
  const setVisibility = async (id: string, visibility: 'public' | 'private') => {
    try {
      await patchWork(id, { visibility })
      await refreshTree()
      Toast.success(visibility === 'public' ? '已公开：访客可读' : '已设为私有')
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '可见性更新失败')
    }
  }

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
    if (onRuns) return [ROOT_CRUMB, { name: 'Altas 工单' }]
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
        {me.authed && onDoc && (
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
        {/* 可见性（对齐飞书母本的锁图标）：私有/公开直接改当前节点 */}
        {me.authed && onDoc && current && (
          <Dropdown
            trigger="click"
            position="bottomRight"
            render={
              <Dropdown.Menu>
                <Dropdown.Item
                  icon={<IconGlobe size="small" />}
                  onClick={() => void setVisibility(current.id, 'public')}
                >
                  公开：访客可读
                </Dropdown.Item>
                <Dropdown.Item
                  icon={<IconLock size="small" />}
                  onClick={() => void setVisibility(current.id, 'private')}
                >
                  私有：仅登录可见
                </Dropdown.Item>
              </Dropdown.Menu>
            }
          >
            {/* 注意：这里不要再套 Tooltip —— Dropdown 的 trigger 只认直接子元素，
                包一层后点击事件被吃掉，菜单永远弹不出来。 */}
            <Button
              theme="borderless"
              type="tertiary"
              size="small"
              icon={current.visibility === 'public' ? <IconGlobe /> : <IconLock />}
              aria-label={current.visibility === 'public' ? '可见性：公开' : '可见性：私有'}
            />
          </Dropdown>
        )}
        {me.authed && (
        <Tooltip content="问 Altas" position="bottom">
          <Button
            className="ai-topbar-btn"
            /* 紫色不靠内联：`.ai-topbar-btn` 在元素作用域内把 Semi 读的
               --semi-color-text-1 / --semi-color-tertiary-hover 换成 AI 紫，
               连 hover/active 一起生效（见 index.css 注释）。 */
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconAIFilledLevel1 />}
            onClick={() => setAiPanelOpen(true)}
          >
            问 Altas
          </Button>
        </Tooltip>
        )}
        {me.authed ? (
        <Dropdown
          trigger="click"
          position="bottomRight"
          render={
            <Dropdown.Menu>
              <Dropdown.Item onClick={() => nav('/runs')}>Altas 工单</Dropdown.Item>
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
        ) : (
          <Button
            theme="solid"
            type="primary"
            size="small"
            onClick={() => nav('/login')}
          >
            登录
          </Button>
        )}
        {me.authed && (
          <Dropdown
            trigger="click"
            position="bottomRight"
            render={
              <Dropdown.Menu>
                <Dropdown.Item disabled>{me.username}</Dropdown.Item>
                <Dropdown.Divider />
                <Dropdown.Item onClick={() => void doLogout()}>退出登录</Dropdown.Item>
              </Dropdown.Menu>
            }
          >
            <Avatar size="extra-small" color="light-blue">
              {(me.username || '我').slice(0, 1).toUpperCase()}
            </Avatar>
          </Dropdown>
        )}
      </div>
    </header>
  )
}
