import type { Register } from 'claude-code'

// Only a merge is ever worth a check: anything else goes to the default
// flow without spawning anything.
const MERGE_HINT = /gh\s+pr\s+merge\s+\d+/

// The manager's own binary, through the environment every managed session
// carries. Resolved through the shell so the module never guesses a path.
async function managerBin($: {
  process: { run: (argv: readonly string[], init?: { timeoutMs?: number }) => Promise<{ exitCode: number; stdout: string }> }
}): Promise<string> {
  try {
    const found = await $.process.run(['sh', '-c', 'echo -n ${GATE_INBOX_BIN:-}'], { timeoutMs: 10000 })
    if (found.exitCode === 0 && found.stdout.trim() !== '') return found.stdout.trim()
  } catch {
    // Fall through to the default flow below.
  }
  return ''
}

export const register: Register = on => {
  on('tool.check', { tool: 'Bash' }, async ($, e, next) => {
    const command = (e.input as { command?: string }).command ?? ''
    if (!MERGE_HINT.test(command)) return next(e)
    const bin = await managerBin($)
    if (bin === '') return next(e)
    try {
      const checked = await $.process.run(
        [bin, 'merge-gate-check', '--command', command],
        { timeoutMs: 25000 },
      )
      if (checked.exitCode === 0) {
        return {
          decision: 'allow',
          reason: checked.stdout.trim() || 'Gate Inbox merge gate: the standing merge authority covers this merge.',
        }
      }
    } catch {
      // Fall through to the default flow below.
    }
    return next(e)
  }).catch(($, e, next) => next(e))
}
