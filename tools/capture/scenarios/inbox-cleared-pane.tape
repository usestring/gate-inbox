# Draining triage opens the inbox-cleared pane beside the list rather than
# the full-screen new-session form.
# Run with
#   --no-seed
#
# The board opens in triage with nothing to hand over, so the pane takes the
# content column: the art, one prompt, the CLI picker and the launch/dismiss
# keys. tab steps the CLI; esc dismisses back to the list, still in triage.

Sleep 4s
Screenshot {{SHOTS}}/01-inbox-cleared-pane.png
Sleep 1s

Type "draft the release notes"
Sleep 1500ms
Tab
Sleep 1500ms
Screenshot {{SHOTS}}/02-prompt-and-cli-picker.png
Sleep 1s

Escape
Sleep 2s
Screenshot {{SHOTS}}/03-dismissed-back-to-the-list.png
Sleep 2s
