import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Banner, Button, Card, Input, Toast, Typography } from '@douyinfe/semi-ui'
import { IconLock } from '@douyinfe/semi-icons'
import { login } from '../lib/api'
import { useAppStore } from '../lib/store'

const { Title, Text } = Typography

/**
 * 访问密码（软门槛）：用于挡一下不适合公开展示的内容（比如以后的 Gal 条目），
 * 不是安全边界。真正的公开/私有边界在节点可见性上（visitor 只能读 public）。
 */
export default function LoginView() {
  const nav = useNavigate()
  const { refreshMe, me, meLoaded } = useAppStore()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [lockedFor, setLockedFor] = useState<number | null>(null)

  const submit = async () => {
    if (!password) return
    setBusy(true)
    try {
      await login(password)
      await refreshMe()
      Toast.success('已进入管理态')
      nav('/')
    } catch (e) {
      const msg = e instanceof Error ? e.message : '访问密码不正确'
      const locked = /尝试次数过多/.exec(msg)
      if (locked) {
        setLockedFor(Number(/(\d+)/.exec(msg)?.[1] ?? 300))
      }
      Toast.error(msg)
    } finally {
      setBusy(false)
      setPassword('')
    }
  }

  return (
    <div className="login-view">
      <Card className="login-card">
        <div className="login-brand">
          <span className="brand-mark">W</span>
        </div>
        <Title heading={4} style={{ margin: '0 0 4px', textAlign: 'center' }}>
          WikiAltas
        </Title>
        <Text type="tertiary" size="small" className="login-hint">
          访客可读公开文档，输入访问密码进入管理态。
        </Text>

        {meLoaded && !me.adminPasswordConfigured && (
          <Banner
            type="info"
            closeIcon={null}
            style={{ margin: '12px 0' }}
            description="尚未设置访问密码"
          />
        )}

        <div className="login-field">
          {/* 只用 Semi 原生的密码态：`mode="password"` 自己就带显隐按钮。
              再塞一个 suffix 会渲染出**两个眼睛图标**干同一件事（实测踩到）。 */}
          <Input
            mode="password"
            value={password}
            prefix={<IconLock />}
            placeholder="请输入访问密码"
            autoFocus
            disabled={lockedFor != null}
            onChange={setPassword}
            onEnterPress={() => void submit()}
          />
        </div>

        {lockedFor != null && (
          <Text type="tertiary" size="small">
            尝试次数过多，请 {Math.ceil(lockedFor / 60)} 分钟后再试
          </Text>
        )}

        <Button
          theme="solid"
          type="primary"
          block
          style={{ marginTop: 12 }}
          loading={busy}
          disabled={!password || lockedFor != null}
          onClick={() => void submit()}
        >
          进入管理态
        </Button>
        <Button theme="borderless" block style={{ marginTop: 8 }} onClick={() => nav('/')}>
          以访客身份浏览
        </Button>
        <Text type="tertiary" size="small" className="login-foot">
          未登录只能读公开条目，私有条目完全不可见。
        </Text>
      </Card>
    </div>
  )
}
