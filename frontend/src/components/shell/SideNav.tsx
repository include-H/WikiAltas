import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Button, Tooltip } from '@douyinfe/semi-ui'
import {
  IconAppCenter,
  IconHome,
  IconMoon,
  IconPlus,
  IconSearch,
  IconSetting,
  IconSidebar,
  IconStar,
  IconSun,
} from '@douyinfe/semi-icons'
import { useAppStore } from '../../lib/store'
import { toggleTheme } from '../../lib/theme'
import { workPath } from '../../lib/routes'
import SearchModal from './SearchModal'
import WorkTree from './WorkTree'
import FolderPane from './FolderPane'
import { CreateNodeModal } from './WorkDialogs'
import type { CreateTarget } from './WorkDialogs'

export default function SideNav() {
  const nav = useNavigate()
  const loc = useLocation()
  const { nodes, pinnedIds, sidebarCollapsed, setSidebarCollapsed, setAiPanelOpen } = useAppStore()
  const [searchOpen, setSearchOpen] = useState(false)
  const [createRoot, setCreateRoot] = useState<CreateTarget | null>(null)
  const [theme, setTheme] = useState(
    document.body.getAttribute('theme-mode') === 'dark' ? 'dark' : 'light',
  )

  const pinned = nodes.filter((n) => pinnedIds.includes(n.id))
  // 资料夹模式：路由在 /w/:id/folder(/:docId) 时，左栏从宇宙树切成该系列的资料列表
  const folderWorkId = /^\/w\/([^/]+)\/folder/.exec(loc.pathname)?.[1] ?? null
  const folderMode =
    !!folderWorkId && folderWorkId !== '-' && nodes.some((n) => n.id === folderWorkId)

  if (sidebarCollapsed) {
    return (
      <div className="side-nav collapsed">
        <Tooltip content="WikiAltas 首页" position="right">
          <Button
            className="brand-mark"
            theme="solid"
            type="primary"
            aria-label="WikiAltas 首页"
            onClick={() => nav('/')}
          >
            W
          </Button>
        </Tooltip>
        <Tooltip content="搜索" position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconSearch />}
            onClick={() => setSearchOpen(true)}
          />
        </Tooltip>
        <Tooltip content="首页" position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconHome />}
            onClick={() => nav('/')}
          />
        </Tooltip>
        <Tooltip content="新建宇宙" position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconPlus />}
            onClick={() => setSidebarCollapsed(false)}
          />
        </Tooltip>
        <Tooltip content="馆员" position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconAppCenter />}
            onClick={() => setAiPanelOpen(true)}
          />
        </Tooltip>
        <div className="side-spacer" />
        <Tooltip content={theme === 'dark' ? '浅色模式' : '深色模式'} position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={theme === 'dark' ? <IconSun /> : <IconMoon />}
            onClick={() => setTheme(toggleTheme())}
          />
        </Tooltip>
        <Tooltip content="设置" position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconSetting />}
            onClick={() => nav('/settings')}
          />
        </Tooltip>
        <Tooltip content="展开侧边栏" position="right">
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconSidebar />}
            onClick={() => setSidebarCollapsed(false)}
          />
        </Tooltip>
        <SearchModal open={searchOpen} onClose={() => setSearchOpen(false)} />
      </div>
    )
  }

  return (
    <div className="side-nav">
      <div className="side-brand">
        <span className="brand-mark">W</span>
        <span className="brand-name">WikiAltas</span>
      </div>

      <Button
        className="side-search"
        theme="light"
        type="tertiary"
        icon={<IconSearch />}
        onClick={() => setSearchOpen(true)}
      >
        搜索
      </Button>

      <nav className="side-links">
        <Button
          className={`side-link${loc.pathname === '/' ? ' active' : ''}`}
          theme="borderless"
          type="tertiary"
          icon={<IconHome />}
          onClick={() => nav('/')}
        >
          首页
        </Button>
        <Button
          className="side-link"
          theme="borderless"
          type="tertiary"
          icon={<IconStar />}
          onClick={() => nav('/')}
        >
          置顶
        </Button>
      </nav>

      <div className="side-section">
        <div className="side-section-head">置顶知识库</div>
        {pinned.map((n) => (
          <Button
            key={n.id}
            className="side-doc"
            theme="borderless"
            type="tertiary"
            icon={<IconStar size="small" />}
            onClick={() => nav(workPath(n.id, n.slug))}
          >
            {n.title}
          </Button>
        ))}
        <Button
          className="side-add"
          theme="borderless"
          type="tertiary"
          icon={<IconPlus size="small" />}
          onClick={() => setCreateRoot({ id: null, title: '', kind: null })}
        >
          新建或置顶知识库
        </Button>
      </div>

      <div className="side-section side-section-grow">
        <div className="side-section-head">
          {folderMode ? '资料夹' : '我的文档库'}
        </div>
        <div className="side-tree-scroll">
          {folderMode && folderWorkId ? <FolderPane workId={folderWorkId} /> : <WorkTree />}
        </div>
      </div>

      <div className="side-foot">
        <span className="side-foot-hint">
          {nodes.length} 节点
          {pinned.length > 0 ? ` · 置顶 ${pinned.length}` : ''}
        </span>
        <Tooltip content="折叠侧边栏" position="top">
          <Button
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconSidebar />}
            onClick={() => setSidebarCollapsed(true)}
          />
        </Tooltip>
      </div>

      <SearchModal open={searchOpen} onClose={() => setSearchOpen(false)} />
      <CreateNodeModal target={createRoot} onClose={() => setCreateRoot(null)} />
    </div>
  )
}
