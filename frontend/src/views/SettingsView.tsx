import { useEffect, useState } from 'react'
import {
  Banner,
  Button,
  Form,
  Input,
  InputNumber,
  Spin,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import type { Settings } from '../types'
import { getSettings, putSettings } from '../lib/api'

const { Title } = Typography

export default function SettingsView() {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [settings, setSettings] = useState<Settings | null>(null)
  const [form, setForm] = useState({
    endpoint: '',
    model: '',
    apiKey: '',
    embyUrl: '',
    embyApiKey: '',
    komgaUrl: '',
    komgaApiKey: '',
    gameatlasUrl: '',
    gameatlasApiKey: '',
    expireDays: 7,
    keepEventsDays: 90,
    skillRoot: '',
  })

  useEffect(() => {
    void (async () => {
      setLoading(true)
      try {
        const s = await getSettings()
        setSettings(s)
        setForm({
          endpoint: s.llm?.endpoint ?? '',
          model: s.llm?.model ?? '',
          apiKey: '',
          embyUrl: s.library?.embyUrl ?? '',
          embyApiKey: s.library?.embyApiKey ?? '',
          komgaUrl: s.library?.komgaUrl ?? '',
          komgaApiKey: s.library?.komgaApiKey ?? '',
          gameatlasUrl: s.library?.gameatlasUrl ?? '',
          gameatlasApiKey: s.library?.gameatlasApiKey ?? '',
          expireDays: s.runs?.expireDays ?? 7,
          keepEventsDays: s.runs?.keepEventsDays ?? 90,
          skillRoot: s.skillRoot ?? '',
        })
        setError(null)
      } catch (e) {
        setError(e instanceof Error ? e.message : '加载设置失败')
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  const save = async () => {
    setSaving(true)
    try {
      const body: Partial<Settings> = {
        llm: {
          endpoint: form.endpoint,
          model: form.model,
          apiKeyConfigured: settings?.llm?.apiKeyConfigured ?? false,
        },
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
        },
        skillRoot: form.skillRoot,
      }
      if (form.apiKey) {
        // only send key if user typed a new one
        ;(body.llm as Record<string, unknown>).apiKey = form.apiKey
      }
      const s = await putSettings(body)
      setSettings(s)
      Toast.success('设置已保存')
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <Spin style={{ display: 'block', margin: 64 }} />

  return (
    <div className="settings-view">
      <Title heading={4}>设置</Title>
      {error && <Banner type="warning" description={error} style={{ marginBottom: 16 }} />}

      <Form labelPosition="top">
        <Title heading={6} style={{ marginTop: 24 }}>
          LLM（OpenAI 兼容）
        </Title>
        <Form.Label>Endpoint</Form.Label>
        <Input
          value={form.endpoint}
          onChange={(v) => setForm((f) => ({ ...f, endpoint: v }))}
          placeholder="https://api.openai.com/v1"
        />
        <Form.Label>Model</Form.Label>
        <Input
          value={form.model}
          onChange={(v) => setForm((f) => ({ ...f, model: v }))}
          placeholder="gpt-4o"
        />
        <Form.Label>
          API Key{' '}
          {settings?.llm?.apiKeyConfigured ? (
            <span style={{ opacity: 0.6, fontWeight: 400 }}>(已配置，留空则不修改)</span>
          ) : (
            <span style={{ opacity: 0.6, fontWeight: 400 }}>(未配置)</span>
          )}
        </Form.Label>
        <Input
          value={form.apiKey}
          onChange={(v) => setForm((f) => ({ ...f, apiKey: v }))}
          placeholder="sk-..."
          mode="password"
        />

        <Title heading={6} style={{ marginTop: 24 }}>
          媒体库
        </Title>
        <Form.Label>Emby URL</Form.Label>
        <Input
          value={form.embyUrl}
          onChange={(v) => setForm((f) => ({ ...f, embyUrl: v }))}
          placeholder="http://emby.local:8096"
        />
        <Form.Label>Emby API Key</Form.Label>
        <Input
          value={form.embyApiKey}
          onChange={(v) => setForm((f) => ({ ...f, embyApiKey: v }))}
          mode="password"
        />
        <Form.Label>Komga URL</Form.Label>
        <Input
          value={form.komgaUrl}
          onChange={(v) => setForm((f) => ({ ...f, komgaUrl: v }))}
          placeholder="http://komga.local:25600"
        />
        <Form.Label>Komga API Key</Form.Label>
        <Input
          value={form.komgaApiKey}
          onChange={(v) => setForm((f) => ({ ...f, komgaApiKey: v }))}
          mode="password"
        />
        <Form.Label>GameAtlas URL</Form.Label>
        <Input
          value={form.gameatlasUrl}
          onChange={(v) => setForm((f) => ({ ...f, gameatlasUrl: v }))}
        />
        <Form.Label>GameAtlas API Key</Form.Label>
        <Input
          value={form.gameatlasApiKey}
          onChange={(v) => setForm((f) => ({ ...f, gameatlasApiKey: v }))}
          mode="password"
        />

        <Title heading={6} style={{ marginTop: 24 }}>
          工单与清理
        </Title>
        <Form.Label>Run 过期天数</Form.Label>
        <InputNumber
          value={form.expireDays}
          min={1}
          max={365}
          onNumberChange={(v) => setForm((f) => ({ ...f, expireDays: Number(v ?? 7) }))}
          style={{ width: 200 }}
        />
        <Form.Label>事件保留天数</Form.Label>
        <InputNumber
          value={form.keepEventsDays}
          min={1}
          max={365}
          onNumberChange={(v) => setForm((f) => ({ ...f, keepEventsDays: Number(v ?? 90) }))}
          style={{ width: 200 }}
        />

        <Title heading={6} style={{ marginTop: 24 }}>
          Skill
        </Title>
        <Form.Label>Skill Root</Form.Label>
        <Input
          value={form.skillRoot}
          onChange={(v) => setForm((f) => ({ ...f, skillRoot: v }))}
          placeholder=".claude/skill/wiki-writing/"
        />

        <div style={{ marginTop: 24 }}>
          <Button theme="solid" type="primary" loading={saving} onClick={() => void save()}>
            保存设置
          </Button>
        </div>
      </Form>
    </div>
  )
}
