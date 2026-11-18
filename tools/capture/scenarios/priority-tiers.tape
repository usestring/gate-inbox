# The default scale is two tiers above the default and one below, so ctrl+p
# steps high, medium, low and back to none. Settings' "priority tiers" row
# then widens the scale to the full +3 / -3.

Screenshot {{SHOTS}}/01-default.png
Sleep 1s
Down
Sleep 600ms
Down
Sleep 900ms
Ctrl+p
Sleep 1500ms
Screenshot {{SHOTS}}/02-high.png
Sleep 1s
Ctrl+p
Sleep 1500ms
Screenshot {{SHOTS}}/03-medium.png
Sleep 1s
Ctrl+p
Sleep 1500ms
Screenshot {{SHOTS}}/04-low.png
Sleep 1s
Ctrl+p
Sleep 1500ms
Screenshot {{SHOTS}}/05-default.png
Sleep 1s

# "priority tiers" is the twelfth field, eleven rows below the first.
# vhs cannot type ctrl+alt+s, so Settings opens from quick actions.
Ctrl+k
Sleep 1500ms
Type "settings"
Sleep 1s
Enter
Sleep 2s
Down 11
Sleep 1s
Screenshot {{SHOTS}}/06-scale-default.png
Sleep 1s
Right 6
Sleep 1s
Screenshot {{SHOTS}}/07-scale-widened.png
Sleep 1s
Enter
Sleep 1500ms

# On +3 / -3 the cycle starts at urgent again.
Ctrl+p
Sleep 1500ms
Screenshot {{SHOTS}}/08-urgent-on-full-scale.png
Sleep 1s
