# The status jumps: one key into the next session in a given state, and back
# out again. The driver has seeded the board with sessions resting in
# different states, which is the whole precondition this scenario needs.
#
# The cursor starts on the last session the seeding created, so every jump
# below crosses the list rather than stepping one row.

Screenshot {{SHOTS}}/01-board.png
Sleep 1500ms

# tab: the next session that needs a person at all, whatever its state.
Tab
Sleep 2500ms
Screenshot {{SHOTS}}/02-tab-entered-waiting.png
Sleep 1500ms
Ctrl+q
Sleep 2s

# alt+w names one state instead. Same session here, reached by asking for
# waiting rather than for whoever is next in the queue.
Alt+w
Sleep 2500ms
Screenshot {{SHOTS}}/03-alt-w-entered-waiting.png
Sleep 1500ms
Ctrl+q
Sleep 2s

# alt+i walks to what is resting rather than to what is blocked -- the key a
# board with nothing waiting is read with.
Alt+i
Sleep 2500ms
Screenshot {{SHOTS}}/04-alt-i-entered-idle.png
Sleep 1500ms
Ctrl+q
Sleep 2s

# A state nothing on the board is in says so, rather than reading as a key
# that did nothing. The bar dismisses itself after a couple of polls, so this
# still is taken while it is up.
Alt+f
Sleep 700ms
Screenshot {{SHOTS}}/05-nothing-is-finished.png
Sleep 1500ms

# The key map carries the family, which is where an operator meets it.
Type "?"
Sleep 2s
Screenshot {{SHOTS}}/06-key-map.png
Sleep 1500ms
Escape
Sleep 1s
