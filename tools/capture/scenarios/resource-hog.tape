# The board telling a session its processes are holding the machine. Run with
#   --config tools/capture/scenarios/resource-hog.toml
#
# The config drops the memory notice to half a MiB, which every demo agent's
# tree is over, so each row trips it on the watcher's first sample. The row
# wears "mem hog" beside its status, and a session at a prompt that can take a
# message is typed a notice fenced under Gate Inbox's own band, naming the
# heaviest processes. The waiting session keeps its dialog: the notice queues
# behind it like any message.

Sleep 20s
Screenshot {{SHOTS}}/01-rows-badged-mem-hog.png
Sleep 2s

Enter
Sleep 4s
Screenshot {{SHOTS}}/02-the-notice-in-the-pane.png
Sleep 2s
Ctrl+q
Sleep 2s
