# A claude session interrupted at its composer, with claude's staged-update
# banner ("✔ Update installed · Restart to update") drawn under the interrupt
# line. Run with
#   --no-seed --transcripts update-banner,working,finished
#
# The banner used to be the region's last content line, which hid the
# blocked_line interrupt above it: the row settled at FINISHED for a session
# that was actually waiting on the operator. It now reads WAITING.
#
# It seeds itself rather than using the driver's trio, because on a loaded
# machine the board can come up slower than the driver's grace and the welcome
# screen then eats the seeding keys. The opening sleep is long for the same
# reason: the board builds an gate-inbox binary at startup, which took ~60s
# at load average 40 here, and every key before it is up is spent on a shell.

Sleep 30s
Enter
Sleep 4s

Type "n"
Sleep 2s
Type "claude"
Sleep 1500ms
Enter
Sleep 12s
Ctrl+q
Sleep 5s

Screenshot {{SHOTS}}/01-interrupted-row-under-banner.png
Sleep 2s

# The pane the status was derived from: the banner sits under the interrupt
# line, directly above the composer. Identical in both runs -- the row's status
# is the only thing that differs.
Enter
Sleep 3s
Screenshot {{SHOTS}}/02-pane-carrying-the-banner.png
Sleep 2s
Ctrl+q
Sleep 1s
