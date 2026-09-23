# The board comes back from a lost tmux server twice: the first start offers
# the panes, and the second one -- with the offer already answered -- does not.
Type "q"
Sleep 2s

# What a reboot amounts to here: the panes go, the rows stay.
Type "{{TMUX}} kill-server"
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 12s
Screenshot {{SHOTS}}/01-the-offer.png
Sleep 1s

# Dismissed. The rows stay dead, and v or V still brings any of them back.
Escape
Sleep 3s
Screenshot {{SHOTS}}/02-dismissed.png
Sleep 1s

# Start again on the same rows. Before this change the offer came back here.
Type "q"
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 12s
Screenshot {{SHOTS}}/03-no-second-offer.png
Sleep 2s
