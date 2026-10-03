# The key map under h, then its legend under l: session marks and work marks
# in their own colors, and back to keys with the same key.

Type "h"
Sleep 2s
Screenshot {{SHOTS}}/01-key-map.png
Sleep 1s

# The search is on when the map opens, so leave it before l can reach the legend.
Enter
Sleep 600ms
Type "l"
Sleep 2s
Screenshot {{SHOTS}}/02-legend.png
Sleep 1s

Type "l"
Sleep 2s
Screenshot {{SHOTS}}/03-key-map-again.png
Sleep 1s
