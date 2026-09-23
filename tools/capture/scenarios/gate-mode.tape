# The gate: one key arms the drain, and the queue is then walked a session at
# a time, full width, with no rail beside it. Recorded against the seeded
# trio, which already has a session waiting on somebody:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/gate-mode.tape

# Let the seeded transcripts play out first, so the statuses the queue orders
# on are the ones on screen when it is armed.
Sleep 12s
Screenshot {{SHOTS}}/01-board-with-the-rail.png
Sleep 1s

# G arms all three at once -- queue, handover, full width -- and opens the
# session at the head of the queue rather than leaving the cursor on a list.
Type "G"
Sleep 3s
Screenshot {{SHOTS}}/02-gate-armed-on-the-head.png
Sleep 1s

# alt+. is the skip: this one wants nothing, so it comes off the queue from
# inside the pane and the next one promotes itself.
#
# Spelled as escape-then-dot because vhs 0.11's own Alt+<char> sends the bare
# character with no escape byte in front of it -- Alt+a records as "a" -- so
# every alt chord this board has is unrecordable written the way it is read.
# The two commands go down the pty back to back with no sleep between them,
# which is the escape prefix an alt chord is.
Escape
Type "."
Sleep 3s
Screenshot {{SHOTS}}/03-skipped-to-the-next.png
Sleep 1s

# alt+l is the step back the gate shares with v1's `,`: the session just
# skipped reopens and its mute is lifted. The footer beside it names the rest
# of the session controls -- end, launch, copy id, editor -- and alt+, hides
# the row. Spelled escape-then-l for the same reason as alt+.
Escape
Type "l"
Sleep 3s
Screenshot {{SHOTS}}/04-back-to-the-skipped-one.png
Sleep 1s

# ctrl+\ stops the gate, and the rail and the queue from before it come back.
Ctrl+\
Sleep 3s
Screenshot {{SHOTS}}/05-stopped-rail-restored.png
Sleep 2s
