import { useState } from 'react'
import { Button, Divider, Input, Modal, Toast, Tooltip } from '@douyinfe/semi-ui'
import {
  IconAIFilledLevel1,
  IconBold,
  IconCode,
  IconComment,
  IconH1,
  IconH2,
  IconH3,
  IconItalic,
  IconLanguage,
  IconLink,
  IconList,
  IconOrderedList,
  IconQuote,
  IconStrikeThrough,
  IconText,
  IconUnderline,
} from '@douyinfe/semi-icons'
import { FormattingToolbar, useBlockNoteEditor, useEditorState } from '@blocknote/react'
import { useAppStore } from '../../lib/store'

type BlockLike = { type: string; props?: Record<string, unknown> }

function TbButton({
  icon,
  label,
  active,
  onClick,
}: {
  icon: React.ReactNode
  label: string
  active?: boolean
  onClick: () => void
}) {
  return (
    <Tooltip content={label} position="top">
      <Button
        theme="borderless"
        type="tertiary"
        size="small"
        className={`tb-btn${active ? ' active' : ''}`}
        icon={icon}
        aria-label={label}
        onMouseDown={(e) => e.preventDefault()}
        onClick={onClick}
      />
    </Tooltip>
  )
}

export default function FeishuToolbar() {
  const editor = useBlockNoteEditor()
  const { askSelection } = useAppStore()
  const [linkOpen, setLinkOpen] = useState(false)
  const [linkUrl, setLinkUrl] = useState('')

  const block = useEditorState({
    editor,
    selector: ({ editor: e }) => e.getTextCursorPosition().block as unknown as BlockLike,
  })
  const styles = useEditorState({
    editor,
    selector: ({ editor: e }) => e.getActiveStyles() as Record<string, unknown>,
  })

  const setBlock = (type: string, props?: Record<string, unknown>) => {
    editor.updateBlock(editor.getTextCursorPosition().block, {
      type,
      props,
    } as never)
    editor.focus()
  }

  const toggle = (style: string) => {
    editor.toggleStyles({ [style]: true } as never)
    editor.focus()
  }

  const level = Number(block.props?.level ?? 0)
  const isHeading = block.type === 'heading'

  const submitLink = () => {
    const url = linkUrl.trim()
    if (url) editor.createLink(url)
    setLinkOpen(false)
    setLinkUrl('')
  }

  /**
   * 飞书式 AI 动作：选段先交给 AI 面板。
   * 「问 Altas」只挂上选段等用户提问；「翻译 / 解释」把说法一起发出去（= 飞书点「翻译」）。
   */
  const ask = (prompt: string, autoSend: boolean) => {
    const text = editor.getSelectedText().trim()
    if (!text) {
      Toast.info('先选中一段正文')
      return
    }
    askSelection({ selection: text, prompt, autoSend })
  }

  return (
    <>
      <FormattingToolbar>
        <TbButton
          icon={<IconAIFilledLevel1 />}
          label="问 Altas（@ 选段）"
          onClick={() => ask('', false)}
        />
        <TbButton
          icon={<IconLanguage />}
          label="翻译这段"
          onClick={() => ask('翻译这段', true)}
        />
        <TbButton
          icon={<IconComment />}
          label="解释这段"
          onClick={() => ask('解释这段', true)}
        />
        <Divider layout="vertical" margin="4px" />
        <TbButton
          icon={<IconH1 />}
          label="一级标题"
          active={isHeading && level === 1}
          onClick={() => setBlock('heading', { level: 1 })}
        />
        <TbButton
          icon={<IconH2 />}
          label="二级标题"
          active={isHeading && level === 2}
          onClick={() => setBlock('heading', { level: 2 })}
        />
        <TbButton
          icon={<IconH3 />}
          label="三级标题"
          active={isHeading && level === 3}
          onClick={() => setBlock('heading', { level: 3 })}
        />
        <TbButton
          icon={<IconText />}
          label="正文"
          active={block.type === 'paragraph'}
          onClick={() => setBlock('paragraph')}
        />
        <Divider layout="vertical" margin="4px" />
        <TbButton
          icon={<IconList />}
          label="无序列表"
          active={block.type === 'bulletListItem'}
          onClick={() => setBlock('bulletListItem')}
        />
        <TbButton
          icon={<IconOrderedList />}
          label="有序列表"
          active={block.type === 'numberedListItem'}
          onClick={() => setBlock('numberedListItem')}
        />
        <TbButton
          icon={<IconQuote />}
          label="引用"
          active={block.type === 'quote'}
          onClick={() => setBlock('quote')}
        />
        <TbButton
          icon={<IconCode />}
          label="代码块"
          active={block.type === 'codeBlock'}
          onClick={() => setBlock('codeBlock')}
        />
        <Divider layout="vertical" margin="4px" />
        <TbButton
          icon={<IconBold />}
          label="加粗"
          active={!!styles.bold}
          onClick={() => toggle('bold')}
        />
        <TbButton
          icon={<IconItalic />}
          label="斜体"
          active={!!styles.italic}
          onClick={() => toggle('italic')}
        />
        <TbButton
          icon={<IconUnderline />}
          label="下划线"
          active={!!styles.underline}
          onClick={() => toggle('underline')}
        />
        <TbButton
          icon={<IconStrikeThrough />}
          label="删除线"
          active={!!styles.strike}
          onClick={() => toggle('strike')}
        />
        <TbButton
          icon={<IconCode />}
          label="行内代码"
          active={!!styles.code}
          onClick={() => toggle('code')}
        />
        <TbButton
          icon={<span className="tb-swatch" />}
          label="高亮"
          active={!!styles.backgroundColor}
          onClick={() => {
            editor.toggleStyles({ backgroundColor: 'yellow' } as never)
            editor.focus()
          }}
        />
        <TbButton
          icon={<IconLink />}
          label="链接"
          onClick={() => {
            setLinkUrl('')
            setLinkOpen(true)
          }}
        />
      </FormattingToolbar>
      <Modal
        title="插入链接"
        visible={linkOpen}
        onCancel={() => setLinkOpen(false)}
        onOk={submitLink}
        okText="插入"
        width={420}
      >
        <Input
          placeholder="https://"
          value={linkUrl}
          onChange={setLinkUrl}
          autoFocus
          onEnterPress={submitLink}
        />
      </Modal>
    </>
  )
}
