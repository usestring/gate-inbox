# Review difficulty: each ask carries a tier guessed from its size, d sets one
# by hand (marked *), and D hands over only reviews up to a tier.
# Run with
#   --reviews
#
# The --reviews fixtures are guessed easy and medium. Four presses of d set
# the first one to hard by hand; two of D filter the tab to up to easy, which
# lifts the easy ask to the top; two more go back to any difficulty.

Hide
Sleep 5s
Ctrl+]
Sleep 2s
Show
Sleep 1s
Screenshot {{SHOTS}}/01-guessed-tiers.png
Sleep 1s

Type "d"
Sleep 1200ms
Type "d"
Sleep 1200ms
Type "d"
Sleep 1200ms
Type "d"
Sleep 1500ms
Screenshot {{SHOTS}}/02-set-by-hand.png
Sleep 1s

Type "D"
Sleep 1500ms
Type "D"
Sleep 2s
Screenshot {{SHOTS}}/03-up-to-easy.png
Sleep 1s

Type "D"
Sleep 1500ms
Type "D"
Sleep 2s
Screenshot {{SHOTS}}/04-any-difficulty-again.png
Sleep 2s
