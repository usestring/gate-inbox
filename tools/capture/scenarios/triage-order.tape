# "triage order" and "walk keep-alives" are rows in Settings, under
# smart triage. The order cycles questions first, priority first, oldest
# first, questions only; keep-alives are out of the drain by default.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/triage-order.tape

Sleep 3s
Type "s"
Sleep 2s
Down 21
Sleep 1500ms
Screenshot {{SHOTS}}/01-triage-order-default.png
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/02-triage-order-priority-first.png
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/03-triage-order-oldest-first.png
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/04-triage-order-questions-only.png
Sleep 1s
Down
Right
Sleep 1500ms
Screenshot {{SHOTS}}/05-keep-alives-in-triage-on.png
Sleep 1s
Escape
Sleep 1500ms
Type "s"
Sleep 2s
Down 21
Sleep 1500ms
Screenshot {{SHOTS}}/06-saved.png
Sleep 1s
Escape
Sleep 1s
