/**
 * 面板"当前在看哪段会话"的本地指针键（localStorage: `wikiatlas.session.<target>`）。
 *
 * 单独抽出来是因为**有两个地方要用它**：AI 面板自己记/取，工单列表的「继续这段对话」
 * 也要写同一个键才能把面板切过去。常量写在两处就会漂移——键名一改，两边都静默失效。
 */
export const SESSION_POINTER = 'wikiatlas.session.'
