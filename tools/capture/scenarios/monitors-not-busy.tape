# A turn that ended with background work still running stays working, not
# finished. Record with the first seeded tool playing the monitors transcript:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/monitors-not-busy.tape \
#     --transcripts monitors,working,finished
#
# The driver leaves the cursor on the last session it created; the monitors
# row is the first, so two steps up select it and the preview shows the
# "· 2 monitors still running" tail beside a row that is still working.

Screenshot {{SHOTS}}/01-board.png
Sleep 1500ms

Up
Sleep 700ms
Up
Sleep 1500ms
Screenshot {{SHOTS}}/02-monitors-row-selected.png
Sleep 1500ms
