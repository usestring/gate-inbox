import type { Register } from 'claude-code'

// Gate Inbox types each message into the pane as a header sentence, a fenced
// body, and sometimes a spawner paragraph, a relay attestation and a seal
// line. This draws that row as one line; ctrl+o (expanded) shows it whole.
//
// Only the drawing changes. The text the model reads stays exactly as sent:
// a mod's prompt.submit runs before the UserPromptSubmit hooks, and
// `hook prompt-submit` checks the seal over that exact text, so a rewrite
// would mark every message unverified.

const FENCE =
  /(----(CROSS-SESSION-MESSAGE|GATE-INBOX-NOTICE|EXTENSION-MESSAGE)-[^\n]*?----)\r?\n([\s\S]*?)\r?\n\1/
const AGENT = /"([^"\n]*)" \(session ([A-Za-z0-9_-]+)\)/
const EXTENSION = /From the "([^"\n]*)" extension/
const SENT = /sent \d{4}-\d\d-\d\d (\d\d:\d\d)/
const PASTE = /^\s*<pasted_content[^>]*>\s*([\s\S]*?)\s*<\/pasted_content[^>]*>\s*$/
const BODY_CHARS = 120

export type Summary = { label: string; body: string; isFromParent: boolean }

// summarize is the one line a Gate Inbox message reads as, or null for any
// other prompt.
export function summarize(raw: string): Summary | null {
  const text = raw.replace(PASTE, '$1')
  const fenced = FENCE.exec(text)
  if (!fenced || !text.includes('[gate-inbox')) {
    return null
  }
  const [whole, , kind, inner = ''] = fenced
  const header = text.slice(0, fenced.index)
  const after = text.slice(fenced.index + whole.length)
  const time = SENT.exec(header)?.[1]

  let who: string
  if (kind === 'GATE-INBOX-NOTICE') {
    who = 'notice'
  } else if (kind === 'EXTENSION-MESSAGE') {
    who = `ext ${EXTENSION.exec(header)?.[1] ?? '?'}`
  } else {
    const agent = AGENT.exec(header)
    who = agent ? `${agent[1]} (${agent[2]})` : 'agent'
  }

  const flat = inner.replace(/\s+/g, ' ').trim()
  const body = flat.length > BODY_CHARS ? `${flat.slice(0, BODY_CHARS - 1)}…` : flat

  return {
    label: `⇄ ${who}${time ? ` ${time}` : ''}`,
    body,
    isFromParent: after.includes('session that spawned you'),
  }
}

export const register: Register = on => {
  on('ui.render', { component: 'UserMessage' }, ($, e, next) => {
    if (e.props.isExpanded) {
      return next(e)
    }
    const summary = summarize(e.props.text)
    if (!summary) {
      return next(e)
    }
    const { Box, Text } = $.ui.resolve(e)
    return Box({
      children: Text({
        dimColor: true,
        wrap: 'truncate-end',
        children: [
          Text({ color: 'cyan', children: summary.label }),
          summary.isFromParent ? ' ↑parent' : '',
          ` · ${summary.body}`,
        ],
      }),
    })
  })
}
