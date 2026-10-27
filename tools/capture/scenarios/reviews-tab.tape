# With the review queue on, the rail carries a Sessions | Reviews tab bar. ]
# switches to the Reviews tab, which lists each ask on two lines and shows the
# selected one in the pane; ] again returns to the sessions.
Sleep 8s
Screenshot {{SHOTS}}/01-sessions-tab.png
Type "]"
Sleep 1500ms
Screenshot {{SHOTS}}/02-reviews-tab.png
Down
Sleep 1s
Screenshot {{SHOTS}}/03-second-ask.png
Down
Sleep 1s
Type "]"
Sleep 1500ms
Screenshot {{SHOTS}}/04-back-to-sessions.png
Sleep 1s
