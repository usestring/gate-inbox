# Run with --no-seed --foreign conversation --foreign token-context
# --foreign slash-opened --cols 140 --rows 30. The slash-opened row keeps the
# name demo-project and wears its opening prompt, "/triage-inbox only the
# last day", as its title, so "triage" and "last*day" can only find it by that
# title. The naming sweep runs every 30s, so wait it out first.
Sleep 40s
Screenshot {{SHOTS}}/01-all-agents.png
Sleep 1s
Type "/triage"
Sleep 2s
Screenshot {{SHOTS}}/02-title-word.png
Sleep 1s
Backspace 6
Type "last*day"
Sleep 2s
Screenshot {{SHOTS}}/03-title-wildcard.png
Sleep 1s
Backspace 8
Type "tlad"
Sleep 2s
Screenshot {{SHOTS}}/04-title-not-fuzzed.png
Sleep 1s
Backspace 4
Escape
Sleep 1s
Escape
Sleep 1s
Screenshot {{SHOTS}}/05-restored-board.png
Sleep 1s
