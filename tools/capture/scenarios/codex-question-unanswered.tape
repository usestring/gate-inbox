# A codex session was asked something and never answered. Its dialog's
# auto-resolution timer has already fired, so there is nothing left on the pane
# to see -- before this change the row settled to idle and the question was
# gone with it. Now the row reads waiting, off the rollout rather than the pane.
#
# Run it with the seeded question:
#   tools/capture/capture.sh --scenario tools/capture/scenarios/codex-question-unanswered.tape \
#     --codex-question expired

# Let the seeded trio's demo transcripts play out, so every pane is quiet and
# each turn has ended. That is the moment the codex question used to vanish.
Sleep 14s
Screenshot {{SHOTS}}/a-codex-row-reads-waiting.png

# Walk onto the codex row: its pane is settled and carries no dialog, and the
# row still says the session is waiting on the operator.
Down
Sleep 2s
Screenshot {{SHOTS}}/b-settled-pane-still-waiting.png
Sleep 1s
