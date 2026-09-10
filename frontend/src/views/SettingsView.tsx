import { useCallback, useEffect, useState } from 'react'
import {
  Banner,
  Button,
  Card,
  Input,
  InputNumber,
  Spin,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import type { RuntimeInfo, Settings } from '../types'
import { getRuntime, getSettings, importEnvSettings, putSettings, testLLM } from '../lib/api'

const { Title, Text } = Typography

interface FormState {
  endpoint: string
  model: string
  apiKey: string
  temperature: string
  maxTokens: string
  exaApiKey: string
  embyUrl: string
  embyApiKey: string
  komgaUrl: string
  komgaApiKey: string
  gameatlasUrl: string
  gameatlasApiKey: string
  expireDays: number
  keepEventsDays: number
  maxConcurrentRuns: number
  skillRoot: string
}

const EMPTY_FORM: FormState = {
  endpoint: '',
  model: '',
  apiKey: '',
  temperature: '',
  maxTokens: '',
  exaApiKey: '',
  embyUrl: '',
  embyApiKey: '',
  komgaUrl: '',
  komgaApiKey: '',
  gameatlasUrl: '',
  gameatlasApiKey: '',
  expireDays: 7,
  keepEventsDays: 90,
  maxConcurrentRuns: 2,
  skillRoot: '',
}

function toForm(st: Settings): FormState {
  return {
    ...EMPTY_FORM,
    endpoint: st.llm.endpoint ?? '',
    model: st.llm.model ?? '',
    temperature: st.llm.temperature != null ? String(st.llm.temperature) : '',
    maxTokens: st.llm.maxTokens != null ? String(st.llm.maxTokens) : '',
    embyUrl: st.library.embyUrl ?? '',
    komgaUrl: st.library.komgaUrl ?? '',
    gameatlasUrl: st.library.gameatlasUrl ?? '',
    expireDays: st.runs.expireDays,
    keepEventsDays: st.runs.keepEventsDays,
    maxConcurrentRuns: st.runs.maxConcurrentRuns,
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
    void load()
  }, [load])

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }))

  const save = async () => {
    setSaving(true)
    try {
      const next = await putSettings({
        llm: {
          endpoint: form.endpoint,
          model: form.model,
          apiKey: form.apiKey || undefined,
          temperature: form.temperature ? Number(form.temperature) : undefined,
          maxTokens: form.maxTokens ? Number(form.maxTokens) : undefined,
        },
        search: { exaApiKey: form.exaApiKey || undefined },
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
        skillRoot: form.skillRoot,
      })
      setSettings(next)
      setForm({ ...toForm(next), apiKey: '', exaApiKey: '', embyApiKey: '', komgaApiKey: '', gameatlasApiKey: '' })
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

  const reimport = async () => {
    try {
      const res = await importEnvSettings()
      setSettings(res.settings)
      setForm(toForm(res.settings))
      Toast.success('已从环境变量重新导入')
      setRuntime(await getRuntime().catch(() => null))
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '导入失败')
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

  return (
    <div className="settings-view">
      <div className="settings-head">
        <Title heading={4} style={{ margin: 0 }}>
          设置
        </Title>
        <div className="settings-head-actions">
          <Button theme="borderless" onClick={() => void reimport()}>
            从环境变量重新导入
          </Button>
          <Button theme="solid" type="primary" loading={saving} onClick={() => void save()}>
            保存
          </Button>
        </div>
      </div>
      <Text type="tertiary" size="small">
        配置存在本地 SQLite（不再依赖 .env）；保存后立刻对下一个工单生效。
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
              <InputNumber value={form.temperature ? Number(form.temperature) : undefined} onChange={(v) => set('temperature', v == null ? '' : String(v))} min={0} max={2} step={0.1} style={{ width: '100%' }} />
            </div>
            <div className="settings-field">
              <span className="settings-label">单次输出上限 (tokens)</span>
              <InputNumber value={form.maxTokens ? Number(form.maxTokens) : undefined} onChange={(v) => set('maxTokens', v == null ? '' : String(v))} min={256} max={131072} step={1024} style={{ width: '100%' }} />
            </div>
          </div>
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
              placeholder={settings?.search.exaApiKeyConfigured ? '已配置，留空不修改' : '未配置时馆员只能靠站内知识，事实会标「待核实」'}
            />
          </div>
          <Text type="tertiary" size="small">
            馆员用它核实发行日期、销量、获奖这类外部事实；结果按 query 缓存 30 天，不重复消耗额度。
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
              <Input value={form.gameatlasUrl} onChange={(v) => set('gameatlasUrl', v)} />
            </div>
            <div className="settings-field">
              <span className="settings-label">
                GameAtlas Key {keyTag(settings?.library.gameatlasApiKeyConfigured)}
              </span>
              <Input mode="password" value={form.gameatlasApiKey} onChange={(v) => set('gameatlasApiKey', v)} placeholder="留空不修改" />
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
  )
}
