# Run with --keys tools/capture/scenarios/experimental-auto-group.keys.toml: vhs cannot send ctrl+alt.
# Settings → Experimental, with Auto group toggled on. The pass itself asks a
# classifier, so the capture shows where it is enabled and what it sends.
Sleep 8s
Alt+s
Sleep 2s
Up
Up
Up
Up
Up
Up
Up
Sleep 1s
Enter
Sleep 1s
Down
Down
Down
Down
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/01-auto-group-enabled.png
Sleep 2s
