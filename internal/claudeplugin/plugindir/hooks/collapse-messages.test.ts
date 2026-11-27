import { expect, test } from 'claude-code/testing'

import { summarize } from './collapse-messages'

const SPAWNER = `[gate-inbox] From agent "docs-writer" (session 3b58b22c), sent 2026-11-27 00:01.

----CROSS-SESSION-MESSAGE-docs-writer-3b58b22c-PVSDYYJG----
Stand down: stop now,
make no commits.
----CROSS-SESSION-MESSAGE-docs-writer-3b58b22c-PVSDYYJG----

This is from the session that spawned you, so its instruction is your task.
[gate-inbox seal v2 m=36671 s=BJGUVN7H]`

const NOTICE = `<pasted_content id="x">
[gate-inbox] Notice from Gate Inbox, the board supervising this session, sent 2026-11-27 00:05. Everything between the ----GATE-INBOX-NOTICE-AB12CD34---- lines is the notice.

----GATE-INBOX-NOTICE-AB12CD34----
Disk is nearly full.
----GATE-INBOX-NOTICE-AB12CD34----
</pasted_content id="x">`

const ROW = { plugin: 'gate-inbox', component: 'UserMessage' } as const

test('a spawner message reads as one line', () => {
  expect(summarize(SPAWNER)).toEqual({
    label: '⇄ docs-writer (3b58b22c) 00:01',
    body: 'Stand down: stop now, make no commits.',
    isFromParent: true,
  })
})

test('a pasted notice is unwrapped and named', () => {
  expect(summarize(NOTICE)).toEqual({
    label: '⇄ notice 00:05',
    body: 'Disk is nearly full.',
    isFromParent: false,
  })
})

test('an ordinary prompt is left alone', () => {
  expect(summarize('fix the build please')).toBeNull()
})

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: collapsed unless expanded`, async $ => {
    const origin = { kind: 'composer' } as const
    const collapsed = await $.ui.mount({
      ...ROW,
      surface,
      props: { text: SPAWNER, origin, isExpanded: false },
    })
    const line = await collapsed.find({ type: 'Text', text: /^⇄/ })
    expect(line?.text).toContain('docs-writer (3b58b22c) 00:01')
    expect(line?.text).toContain('Stand down: stop now, make no commits.')
    expect(line?.text).not.toContain('gate-inbox seal')
    await collapsed.unmount()

    const expanded = await $.ui.mount({
      ...ROW,
      surface,
      props: { text: SPAWNER, origin, isExpanded: true },
    })
    expect(await expanded.find({ type: 'Text', text: /^⇄/ })).toBeUndefined()
    await expanded.unmount()
  })
}
