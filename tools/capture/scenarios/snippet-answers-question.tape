# A submitting snippet sent to a session holding an AskUserQuestion with a
# free-text row is typed into that row and submitted, instead of being refused
# the way any other dialog refuses it. The claude row plays the ask-free-text
# transcript, a dialog that takes its answer the way the real one does:
#
#   tools/capture/capture.sh --transcripts ask-free-text,working,finished \
#     --scenario tools/capture/scenarios/snippet-answers-question.tape
#
# The row is reached by the dialog's own text, since only claude plays it.

Sleep 6s
Type "/"
Sleep 1s
Type "rollout "
Sleep 1500ms
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/01-question-waiting.png
Sleep 1s

# option+j is the first-run "Use your judgment" snippet.
Alt+j
Sleep 800ms
Screenshot {{SHOTS}}/02-answering-with-the-snippet.png
Sleep 3s
Screenshot {{SHOTS}}/03-question-answered.png
Sleep 1s
