import { useEffect, useMemo, useRef, useState } from 'react'

/**
 * 登录页的小人（移植自 GameManager 的 AnimatedCharacters.vue）。
 *
 * 行为：眼睛跟着鼠标转、随机眨眼、输入密码时捂住眼睛、失败时沮丧、成功时高兴。
 * 颜色全部走 Semi token（不用具体色值），尺寸克制，只出现在 /login。
 */
interface Props {
  passwordLength: number
  showPassword?: boolean
  isError?: boolean
  isSuccess?: boolean
}

interface CharacterSpec {
  key: string
  tone: 'primary' | 'ink' | 'amber' | 'green'
  size: number
  eye: number
  pupil: number
  offsetY: number
}

const CHARACTERS: CharacterSpec[] = [
  { key: 'primary', tone: 'primary', size: 68, eye: 16, pupil: 7, offsetY: 6 },
  { key: 'ink', tone: 'ink', size: 52, eye: 13, pupil: 6, offsetY: 0 },
  { key: 'amber', tone: 'amber', size: 60, eye: 15, pupil: 6, offsetY: 4 },
  { key: 'green', tone: 'green', size: 46, eye: 12, pupil: 5, offsetY: 10 },
]

export default function LoginCharacters({
  passwordLength,
  showPassword = false,
  isError = false,
  isSuccess = false,
}: Props) {
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const [look, setLook] = useState({ x: 0, y: 0 })
  const [blink, setBlink] = useState(false)

  // 眼睛跟随鼠标（限制在眼球最大位移内）
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const el = wrapRef.current
      if (!el) return
      const rect = el.getBoundingClientRect()
      const cx = rect.left + rect.width / 2
      const cy = rect.top + rect.height / 2
      const dx = e.clientX - cx
      const dy = e.clientY - cy
      const len = Math.hypot(dx, dy) || 1
      const max = 5
      setLook({ x: (dx / len) * max, y: (dy / len) * max })
    }
    window.addEventListener('mousemove', onMove)
    return () => window.removeEventListener('mousemove', onMove)
  }, [])

  // 随机眨眼
  useEffect(() => {
    let timer: number
    const schedule = () => {
      timer = window.setTimeout(() => {
        setBlink(true)
        window.setTimeout(() => setBlink(false), 150)
        schedule()
      }, 3000 + Math.random() * 3500)
    }
    schedule()
    return () => window.clearTimeout(timer)
  }, [])

  // 正在输入密码且处于隐藏态时捂住眼睛
  const covering = passwordLength > 0 && !showPassword
  const stateClass = useMemo(
    () => `${isError ? ' is-error' : ''}${isSuccess ? ' is-success' : ''}${covering ? ' is-covering' : ''}`,
    [isError, isSuccess, covering],
  )

  return (
    <div className={`login-characters${stateClass}`} ref={wrapRef} aria-hidden>
      <div className="login-characters-scene">
        {CHARACTERS.map((c) => (
          <div
            key={c.key}
            className={`login-character tone-${c.tone}`}
            style={{ width: c.size, height: c.size, marginBottom: c.offsetY }}
          >
            {covering ? (
              <div className="login-character-hands" />
            ) : (
              <div className="login-character-eyes">
                {[0, 1].map((i) => (
                  <span
                    key={i}
                    className={`login-eye${blink ? ' is-blinking' : ''}`}
                    style={{ width: c.eye, height: c.eye }}
                  >
                    <span
                      className="login-pupil"
                      style={{
                        width: c.pupil,
                        height: c.pupil,
                        transform: `translate(${look.x}px, ${look.y}px)`,
                      }}
                    />
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
