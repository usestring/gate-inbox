# Title-only search: tab inside the search field narrows the query to the
# session's name and generated title. "poller" is only on the codex pane, so
# the full search keeps that row with a ≡pane badge and title-only drops it.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/title-only-search.tape --cols 110 --rows 30

Sleep 4s
Ctrl+f
Sleep 1500ms
Screenshot {{SHOTS}}/01-search-field-offers-tab.png
Sleep 1s
Type "poller"
Sleep 2500ms
Screenshot {{SHOTS}}/02-full-search-finds-pane-text.png
Sleep 1s
Tab
Sleep 2s
Screenshot {{SHOTS}}/03-title-only-drops-pane-match.png
Sleep 1s
Ctrl+u
Sleep 1500ms
Screenshot {{SHOTS}}/04-title-only-empty-field.png
Sleep 1s
Tab
Sleep 1500ms
Screenshot {{SHOTS}}/05-back-to-full-search.png
Sleep 1s
