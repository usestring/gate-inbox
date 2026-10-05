# The tool filter: Y narrows the list and the triage queue to one harness,
# cycling alphabetically through the CLIs on the board and back to all.
# The seeding leaves one session per installed CLI, so the cycle here is
# all -> claude -> codex -> opencode -> all.

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# Y filters the list to the first harness alphabetically: claude.
Type "Y"
Sleep 1s
Screenshot {{SHOTS}}/02-filter-claude.png
Sleep 1s

# Y again: the next harness, codex.
Type "Y"
Sleep 1s
Screenshot {{SHOTS}}/03-filter-codex.png
Sleep 1s

# i drains that harness as one queue, opening its head.
Type "i"
Sleep 2s
Screenshot {{SHOTS}}/04-triage-codex-head.png
Sleep 1s

# Back on the list with the drain still armed: the queue itself, codex only.
Ctrl+\
Sleep 2s
Screenshot {{SHOTS}}/05-triage-queue-codex.png
Sleep 1s

# i leaves triage; the list keeps its harness filter. Y steps on to
# opencode, and one more Y shows the whole fleet again.
Type "i"
Sleep 1s
Type "Y"
Sleep 1s
Screenshot {{SHOTS}}/06-filter-opencode.png
Sleep 1s
Type "Y"
Sleep 1s
Screenshot {{SHOTS}}/07-restored-board.png
Sleep 1s
