# Run with --no-seed --foreign keep-warm. The pane's transcript is titled
# "Terraform auto runner" and its last three prompts are the cache warden's
# keep-warms. The naming sweep runs every 30s and the drift rule renames only
# on the same replacement twice, so by 100s a board that read the keep-warms
# as the session's work has renamed the row after them.
Sleep 100s
Screenshot {{SHOTS}}/01-name-after-keep-warms.png
Sleep 2s
