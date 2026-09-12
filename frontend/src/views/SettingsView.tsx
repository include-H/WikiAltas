import { useCallback, useEffect, useState } from 'react'
import {
  Banner,
  Button,
  Card,
  Input,
  InputNumber,
  Select,
  Spin,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import type { RuntimeInfo, Settings } from '../types'
import { getRuntime, getSettings, putSettings, testLLM } from '../lib/api'
import { useAppStore } from '../lib/store'
import { useNavigate } from 'react-router-dom'

const { Title, Text } = Typography

interface FormState {
  endpoint: string
  model: string
  apiKey: string
  effort: string
  temperature: string
  maxTokens: string
  contextWindow: string
  exaApiKey: string
  proxyUrl: string
  embyUrl: string
  embyApiKey: string
  komgaUrl: string
  komgaApiKey: string
  gameatlasUrl: string
  gameatlasApiKey: string
  expireDays: number
  keepEventsDays: number
  maxConcurrentRuns: number
  adminUsername: string
  newNodeVisibility: 'public' | 'private'
  skillRoot: string
}

const EMPTY_FORM: FormState = {
  endpoint: '',
  model: '',
  apiKey: '',
  effort: 'medium',
  temperature: '',
  maxTokens: '',
  contextWindow: '',
  exaApiKey: '',
  proxyUrl: '',
  embyUrl: '',
  embyApiKey: '',
  komgaUrl: '',
  komgaApiKey: '',
  gameatlasUrl: '',
  gameatlasApiKey: '',
  expireDays: 7,
  keepEventsDays: 90,
  maxConcurrentRuns: 2,
  adminUsername: 'admin',
  newNodeVisibility: 'private',
  skillRoot: '',
}

function toForm(st: Settings): FormState {
  return {
    ...EMPTY_FORM,
    endpoint: st.llm.endpoint ?? '',
    model: st.llm.model ?? '',
    effort: st.llm.reasoningEffort || 'medium',
    temperature: st.llm.temperature != null ? String(st.llm.temperature) : '',
    maxTokens: st.llm.maxTokens != null ? String(st.llm.maxTokens) : '',
    contextWindow: st.llm.contextWindow ? String(st.llm.contextWindow) : '',
    proxyUrl: st.search.proxyUrl ?? '',
    embyUrl: st.library.embyUrl ?? '',
    komgaUrl: st.library.komgaUrl ?? '',
    gameatlasUrl: st.library.gameatlasUrl ?? '',
    expireDays: st.runs.expireDays,
    keepEventsDays: st.runs.keepEventsDays,
    maxConcurrentRuns: st.runs.maxConcurrentRuns,
    adminUsername: st.admin?.username ?? 'admin',
    newNodeVisibility: st.admin?.newNodeVisibility ?? 'private',
    skillRoot: st.skillRoot ?? '',
  }
}

export default function SettingsView() {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [runtime, setRuntime] = useState<RuntimeInfo | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<string | null>(null)
  const { me } = useAppStore()
  const nav = useNavigate()
  const [adminPassword, setAdminPassword] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [st, rt] = await Promise.all([getSettings(), getRuntime().catch(() => null)])
      setSettings(st)
      setForm(toForm(st))
      setRuntime(rt)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '加载设置失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (me.authed) void load()
  }, [load, me.authed])

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }))

  const save = async () => {
    setSaving(true)
    try {
      const next = await putSettings({
        llm: {
          endpoint: form.endpoint,
          model: form.model,
          reasoningEffort: form.effort,
          apiKey: form.apiKey || undefined,
          temperature: form.temperature ? Number(form.temperature) : undefined,
          maxTokens: form.maxTokens ? Number(form.maxTokens) : undefined,
          contextWindow: form.contextWindow ? Number(form.contextWindow) : undefined,
        },
        search: { exaApiKey: form.exaApiKey || undefined, proxyUrl: form.proxyUrl },
        library: {
          embyUrl: form.embyUrl || undefined,
          embyApiKey: form.embyApiKey || undefined,
          komgaUrl: form.komgaUrl || undefined,
          komgaApiKey: form.komgaApiKey || undefined,
          gameatlasUrl: form.gameatlasUrl || undefined,
          gameatlasApiKey: form.gameatlasApiKey || undefined,
        },
        runs: {
          expireDays: form.expireDays,
          keepEventsDays: form.keepEventsDays,
          maxConcurrentRuns: form.maxConcurrentRuns,
        },
        admin: {
          username: form.adminUsername,
          newPassword: adminPassword || undefined,
          newNodeVisibility: form.newNodeVisibility,
        },
        skillRoot: form.skillRoot,
      })
      setSettings(next)
      setForm({ ...toForm(next), apiKey: '', exaApiKey: '', embyApiKey: '', komgaApiKey: '', gameatlasApiKey: '' })
      setAdminPassword('')
      Toast.success('已保存（下一个工单即生效，无需重启）')
      setRuntime(await getRuntime().catch(() => null))
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const runTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await testLLM()
      setTestResult(
        res.ok
          ? `✅ ${res.model} 正常（${res.latencyMs}ms）${res.reply ? ` · 回复：${res.reply}` : ''}`
          : `❌ 失败：${res.error ?? '未知错误'}`,
      )
    } catch (e) {
      setTestResult(`❌ ${e instanceof Error ? e.message : '请求失败'}`)
    } finally {
      setTesting(false)
    }
  }

  const keyTag = (configured?: boolean) =>
    configured ? (
      <Tag size="small" color="green">
        已配置
      </Tag>
    ) : (
      <Tag size="small" color="grey">
        未配置
      </Tag>
    )

  if (loading && !settings) {
    return <Spin style={{ display: 'block', margin: '64px auto' }} />
  }

  // 访客不进入设置页（接口本身也会 401，这里给一个明确的入口）
  if (!me.authed) {
    return (
      <div className="settings-view">
        <div className="settings-inner">
          <Banner
            type="info"
            description="设置需要登录后才能查看与修改。"
          />
          <Button style={{ marginTop: 12 }} theme="solid" type="primary" onClick={() => nav('/login')}>
            去登录
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="settings-view">
      {/* 限宽放**内层**：滚动容器（.settings-view）通栏，滚动条落在窗口右边。
          以前 max-width 加在滚动容器自身上，宽屏下容器 900px 居中，
          滚动条悬在屏幕中间、右侧一大片死白（用户截图）。 */}
      <div className="settings-inner">
      <div className="settings-head">
        <Title heading={4} style={{ margin: 0 }}>
          设置
        </Title>
        <div className="settings-head-actions">
          <Button theme="solid" type="primary" loading={saving} onClick={() => void save()}>
            保存
          </Button>
        </div>
      </div>
      <Text type="tertiary" size="small">
        配置存在本地 SQLite；保存后立刻对下一个工单生效。
      </Text>

      <div className="settings-grid">
        <Card title="模型服务（LLM）" className="settings-card">
          <div className="settings-field">
            <span className="settings-label">Endpoint</span>
            <Input value={form.endpoint} onChange={(v) => set('endpoint', v)} placeholder="https://api.openai.com/v1" />
          </div>
          <div className="settings-field">
            <span className="settings-label">模型</span>
            <Input value={form.model} onChange={(v) => set('model', v)} placeholder="gpt-4o-mini / Qwen / deepseek-v4-flash" />
          </div>
          <div className="settings-field-inline">
            <div className="settings-field">
              <span className="settings-label">思考等级</span>
              <Select<string> value={form.effort} onChange={(v) => set('effort', String(v))} style={{ width: '100%' }}>
                <Select.Option value="off">关闭</Select.Option>
                <Select.Option value="low">低</Select.Option>
                <Select.Option value="medium">中</Select.Option>
                <Select.Option value="high">高</Select.Option>
                <Select.Option value="xhigh">超高</Select.Option>
                <Select.Option value="max">最高</Select.Option>
              </Select>
            </div>
          </div>
          <div className="settings-field">
            <span className="settings-label">
              API Key {keyTag(settings?.llm.apiKeyConfigured)}
            </span>
            <Input
              mode="password"
              value={form.apiKey}
              onChange={(v) => set('apiKey', v)}
              placeholder={settings?.llm.apiKeyConfigured ? '已配置，留空不修改' : '粘贴 API Key'}
            />
          </div>
          <div className="settings-field-inline">
            <div className="settings-field">
              <span className="settings-label">Temperature</span>
              <InputNumber
                value={form.temperature ? Number(form.temperature) : undefined}
                onChange={(v) => set('temperature', v == null ? '' : String(v))}
                min={0}
                max={2}
                step={0.1}
                placeholder="默认 0.7"
                style={{ width: '100%' }}
              />
            </div>
            <div className="settings-field">
              <span className="settings-label">单次输出上限 (tokens)</span>
              <InputNumber
                value={form.maxTokens ? Number(form.maxTokens) : undefined}
                onChange={(v) => set('maxTokens', v == null ? '' : String(v))}
                min={256}
                max={131072}
                step={1024}
                placeholder="默认 65536"
                style={{ width: '100%' }}
              />
            </div>
          </div>
          <div className="settings-field">
            <span className="settings-label">上下文窗口 (tokens)</span>
            <InputNumber
              value={form.contextWindow ? Number(form.contextWindow) : undefined}
              onChange={(v) => set('contextWindow', v == null ? '' : String(v))}
              min={1024}
              max={2000000}
              step={1024}
              placeholder="默认 262144"
              style={{ width: '100%' }}
            />
          </div>
          <Text type="tertiary" size="small">
            Temperature 与输出上限只影响新开的工单。**上下文窗口是硬预算**：它是一次会话
            能装下的 token 总数（系统提示 + 全部历史 + 本轮输出）。接近它时 Altas 会压缩历史，
            压完还放不下就发不出消息——所以这个数要填模型的真实值。
          </Text>
          <div className="settings-actions">
            <Button loading={testing} onClick={() => void runTest()}>
              测试连接
            </Button>
            {testResult && <Text size="small">{testResult}</Text>}
          </div>
        </Card>

        <Card title="联网检索（Exa）" className="settings-card">
          <div className="settings-field">
            <span className="settings-label">
              Exa API Key {keyTag(settings?.search.exaApiKeyConfigured)}
            </span>
            <Input
              mode="password"
              value={form.exaApiKey}
              onChange={(v) => set('exaApiKey', v)}
              placeholder={settings?.search.exaApiKeyConfigured ? '已配置，留空不修改' : '未配置时 Altas 只用站内知识，事实标「待核实」'}
            />
          </div>
          <Text type="tertiary" size="small">
            Altas 用它核实发行日期、销量、获奖这类外部事实；结果按 query 缓存 30 天。
          </Text>

          <div className="settings-field" style={{ marginTop: 14 }}>
            <span className="settings-label">网络代理（出外网用）</span>
            <Input
              value={form.proxyUrl}
              onChange={(v) => set('proxyUrl', v)}
              placeholder="http://192.168.1.253:7890（留空 = 不走代理）"
            />
          </div>
          <Text type="tertiary" size="small">
            fetch_url 抓单个网页时会直连；直连超时 Altas 会自动带 useProxy 用这个代理重试同一页。
            检索（Exa）走自己的通道，不受这里影响。
          </Text>
        </Card>

        <Card title="工单与并发" className="settings-card">
          <div className="settings-field-inline">
            <div className="settings-field">
              <span className="settings-label">并发上限（部/次）</span>
              <InputNumber value={form.maxConcurrentRuns} onChange={(v) => set('maxConcurrentRuns', Number(v ?? 2))} min={1} max={8} style={{ width: '100%' }} />
            </div>
            <div className="settings-field">
              <span className="settings-label">中断工单保留（天）</span>
              <InputNumber value={form.expireDays} onChange={(v) => set('expireDays', Number(v ?? 7))} min={1} max={90} style={{ width: '100%' }} />
            </div>
            <div className="settings-field">
              <span className="settings-label">事件保留（天）</span>
              <InputNumber value={form.keepEventsDays} onChange={(v) => set('keepEventsDays', Number(v ?? 90))} min={7} max={365} style={{ width: '100%' }} />
            </div>
          </div>
          <Text type="tertiary" size="small">
            并发数改完立即生效（worker 池固定上限、闸门实时读设置）。批次建档也走这个上限。
          </Text>
        </Card>

        <Card title="写作 Skill" className="settings-card">
          <div className="settings-field">
            <span className="settings-label">Skill 目录</span>
            <Input value={form.skillRoot} onChange={(v) => set('skillRoot', v)} placeholder="/root/WikiAltas/skills/wiki-writing" />
          </div>
          <Text type="tertiary" size="small">
            当前生效：<code>{runtime?.skillRoot || '未解析到'}</code>
            <br />
            create_wiki 加载 SKILL.md → core.md → 一个 media-*.md；运行时默认不注入 examples/。
          </Text>
        </Card>

        <Card title="访问与隐私" className="settings-card">
          <Text type="tertiary" size="small">
            访问密码是管理态的唯一入口：**未登录只能读公开文档，私有内容一律看不到**
            （公开要求自身与所有祖先都是公开，资料继承所属节点）。
          </Text>
          {settings?.admin.usingDefaultPassword && (
            <Banner
              type="warning"
              closeIcon={null}
              style={{ marginTop: 10 }}
              description="当前仍是出厂密码 1234，建议现在就改掉：改完所有旧会话会立即失效。"
            />
          )}
          <div className="settings-field-inline" style={{ marginTop: 10 }}>
            <div className="settings-field">
              <span className="settings-label">管理员标识（仅显示用）</span>
              <Input value={form.adminUsername} onChange={(v) => set('adminUsername', v)} />
              <Text type="tertiary" size="small">
                Altas 会知道它在给谁做事（被问「我是谁」时用得上）。它只是显示名，
                不参与登录、不代表任何权限。
              </Text>
            </div>
            <div className="settings-field">
              <span className="settings-label">
                访问密码 {keyTag(settings?.admin.passwordConfigured)}
              </span>
              <Input
                mode="password"
                value={adminPassword}
                onChange={setAdminPassword}
                placeholder={settings?.admin.passwordConfigured ? '已设置，留空不修改' : '设置一个密码以启用登录'}
              />
            </div>
            <div className="settings-field">
              <span className="settings-label">新节点默认可见性</span>
              <Select<string>
                value={form.newNodeVisibility}
                onChange={(v) => set('newNodeVisibility', String(v) as 'public' | 'private')}
                optionList={[
                  { value: 'private', label: '私有（推荐）' },
                  { value: 'public', label: '公开' },
                ]}
                style={{ width: '100%' }}
              />
            </div>
          </div>
          <Text type="tertiary" size="small">
            单作 / 系列 / 宇宙都可以单独设为公开或私有；把父节点设为私有时，其下所有内容对访客一并隐藏。
          </Text>
        </Card>

        <Card title="媒体库（后置能力）" className="settings-card">
          <Banner
            type="info"
            closeIcon={null}
            description="首期不做 Emby / Komga / GameAtlas 同步，这里先存凭据；同步入口开放后即可直接用。"
          />
          <div className="settings-field-inline">
            <div className="settings-field">
              <span className="settings-label">Emby URL</span>
              <Input value={form.embyUrl} onChange={(v) => set('embyUrl', v)} />
            </div>
            <div className="settings-field">
              <span className="settings-label">
                Emby Key {keyTag(settings?.library.embyApiKeyConfigured)}
              </span>
              <Input mode="password" value={form.embyApiKey} onChange={(v) => set('embyApiKey', v)} placeholder="留空不修改" />
            </div>
          </div>
          <div className="settings-field-inline">
            <div className="settings-field">
              <span className="settings-label">Komga URL</span>
              <Input value={form.komgaUrl} onChange={(v) => set('komgaUrl', v)} />
            </div>
            <div className="settings-field">
              <span className="settings-label">
                Komga Key {keyTag(settings?.library.komgaApiKeyConfigured)}
              </span>
              <Input mode="password" value={form.komgaApiKey} onChange={(v) => set('komgaApiKey', v)} placeholder="留空不修改" />
            </div>
          </div>
          <div className="settings-field-inline">
            <div className="settings-field">
              <span className="settings-label">GameAtlas URL</span>
              <Input value={form.gameatlasUrl} onChange={(v) => set('gameatlasUrl', v)} placeholder="http://192.168.1.4:3000（你的 GameAtlas 地址）" />
            </div>
            <div className="settings-field">
              <span className="settings-label">
                GameAtlas 管理员密码 {keyTag(settings?.library.gameatlasApiKeyConfigured)}
              </span>
              <Input
                mode="password"
                value={form.gameatlasApiKey}
                onChange={(v) => set('gameatlasApiKey', v)}
                placeholder={settings?.library.gameatlasApiKeyConfigured ? '已配置，留空不修改' : '它没有 API Key——填管理员密码'}
              />
            </div>
          </div>
        </Card>

        <Card title="运行状态与已实现能力" className="settings-card">
          <div className="settings-stats">
            <span>模型：{runtime?.model || '—'}</span>
            <span>并发：{runtime?.maxConcurrentRuns ?? '—'}</span>
            <span>节点：{runtime?.stats.works ?? '—'}</span>
            <span>资料：{runtime?.stats.docs ?? '—'}</span>
            <span>版本：{runtime?.stats.revisions ?? '—'}</span>
            <span>工单：{runtime?.stats.runs ?? '—'}（运行中 {runtime?.stats.runningNow ?? 0}）</span>
            <span>事件：{runtime?.stats.runEvents ?? '—'}</span>
          </div>
          <ul className="settings-features">
            {(runtime?.features ?? []).map((f) => (
              <li key={f}>{f}</li>
            ))}
          </ul>
        </Card>
      </div>
      </div>
    </div>
  )
}
