# The gate's footer carries the operator's own snippets: arm the drain and the
# stock answers are named beside the session controls, on the tier's tail row.
# Recorded against the seeded trio, which already has a session waiting.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/gate-snippets.tape

# Let the seeded transcripts play out first, so the statuses the queue orders
# on are the ones on screen when it is armed.
Sleep 12s

# G arms the queue, the full width and the head session in one key; the footer
# under the pane is what this scenario is recording.
Type "G"
Sleep 3s
Screenshot {{SHOTS}}/01-gate-footer-names-the-snippets.png
Sleep 1s

Ctrl+\
Sleep 2s
