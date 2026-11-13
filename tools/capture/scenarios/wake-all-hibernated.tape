# Wake every hibernated session at once. The resting seed is hibernated from
# the CLI, and the board comes back with each one it stopped marked ◓. Only
# the claude demo records a conversation id, so it is the one hibernate stops;
# the codex and opencode rows stay up. Wake-all brings the stopped ones back.
# vhs cannot type a ctrl+alt chord, so the action and the board's quit run
# from quick actions, which names wake-all's ctrl+alt+w key.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/wake-all-hibernated.tape \
#     --transcripts finished,codex-resting,opencode-finished

# The board opens focused on a pane: leave it before quitting.
Sleep 6s
Ctrl+q
Sleep 1500ms
Ctrl+k
Sleep 1500ms
Type "quit (sessions"
Sleep 1500ms
Enter
Sleep 2s
Type "gib-cap-launch hibernate --idle 1s"
Enter
Sleep 6s

Type "{{LAUNCH}}"
Enter
Sleep 8s
Screenshot {{SHOTS}}/01-hibernated-rows.png
Sleep 1s

Ctrl+k
Sleep 1500ms
Type "wake every"
Sleep 1500ms
Screenshot {{SHOTS}}/02-wake-all-in-quick-actions.png
Sleep 1s
Enter
Sleep 800ms
Screenshot {{SHOTS}}/03-woke-them.png
Sleep 8s
Screenshot {{SHOTS}}/04-back-on-their-conversations.png
Sleep 2s
