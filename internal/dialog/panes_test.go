package dialog

// Panes as Claude Code draws them, trimmed to what the status rules read.

// settledPane is a turn that ended with a plain-text question and an empty
// input line: a message typed into it lands.
const settledPane = `● I have read the 909 payload. Which approach should I try first?

✻ Cooked for 12s · 4.1k tokens

────────────────────────────────────────────
❯
────────────────────────────────────────────
`

// dialogPane is an AskUserQuestion dialog. Its keybinding legend is the
// signal: the input line is gone, and a message typed here would answer the
// dialog with whatever key the first character happens to be.
const dialogPane = `● Which approach should I try first?

  ❯ 1. Reproduce the crypto natively
    2. Keep the vendor solver

  Enter to select · ↑/↓ to navigate · Esc to cancel
`
