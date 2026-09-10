import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Banner, Button, Card, Input, Toast, Typography } from '@douyinfe/semi-ui'
import { IconEyeClosed, IconEyeOpened, IconLock } from '@douyinfe/semi-icons'
import { login } from '../lib/api'
import { useAppStore } from '../lib/store'
import LoginCharacters from '../components/login/LoginCharacters'

const { Title, Text } = Typography

/**
 * 访问密码（软门槛）：用于挡一下不适合公开展示的内容（比如以后的 Gal 条目），
 * 不是安全边界。真正的公开/私有边界在节点可见性上（visitor 只能读 public）。
 */
export default function LoginView() {
  const nav = useNavigate()
  const { refreshMe, me, meLoaded } = useAppStore()
  const [password, setPassword] = useState('')
  const [show, setShow] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  const [remaining] = useState<number | null>(null)
  const [lockedFor, setLockedFor] = useState<number | null>(null)

  const submit = async () => {
    if (!password) return
    setBusy(true)
    setError(false)
    try {
      await login(password)
      await refreshMe()
      Toast.success('已进入管理态')
      nav('/')
    } catch (e) {
      setError(true)
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
        <LoginCharacters
          passwordLength={password.length}
          showPassword={show}
          isError={error}
        />
        <Title heading={4} style={{ margin: '0 0 4px', textAlign: 'center' }}>
          WikiAltas
        </Title>
        <Text type="tertiary" size="small" className="login-hint">
          未登录可以自由阅读「公开」文档；输入访问密码后进入管理态（写、馆员、设置）。
        </Text>

        {meLoaded && !me.adminPasswordConfigured && (
          <Banner
            type="info"
            closeIcon={null}
            style={{ margin: '12px 0' }}
            description="还没有设置访问密码：先在本机设置页设一个，之后就能用它进入管理态。"
          />
        )}

        <div className="login-field">
          <Input
            mode={show ? undefined : 'password'}
            value={password}
            prefix={<IconLock />}
            suffix={
              <Button
                theme="borderless"
                type="tertiary"
                size="small"
                icon={show ? <IconEyeOpened /> : <IconEyeClosed />}
                aria-label={show ? '隐藏密码' : '显示密码'}
                onClick={() => setShow((v) => !v)}
              />
            }
            placeholder="请输入访问密码"
            autoFocus
            disabled={lockedFor != null}
            onChange={setPassword}
            onEnterPress={() => void submit()}
          />
        </div>

        {(remaining != null || lockedFor != null) && (
          <Text type="tertiary" size="small">
            {lockedFor != null
              ? `尝试次数过多，请 ${Math.ceil(lockedFor / 60)} 分钟后再试`
              : `剩余尝试次数：${remaining}`}
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
          以访客身份继续阅读
        </Button>
        <Text type="tertiary" size="small" className="login-foot">
          说明：这是一道「别让不该公开的东西被顺路看到」的软门槛，不用于保护敏感数据。
        </Text>
      </Card>
    </div>
  )
}
