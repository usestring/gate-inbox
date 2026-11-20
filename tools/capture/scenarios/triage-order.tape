# All three triage orders respect priority. Settings search keeps the fixture
# independent of how many other settings are above the order picker.

Sleep 2s
Escape
Ctrl+S
Sleep 1s
Type "/"
Type "triage order"
Sleep 1s
Screenshot {{SHOTS}}/01-priority-then-questions.png
Enter
Right
Sleep 1s
Screenshot {{SHOTS}}/02-priority-then-oldest.png
Right
Sleep 1s
Screenshot {{SHOTS}}/03-priority-questions-only.png
Sleep 1s
