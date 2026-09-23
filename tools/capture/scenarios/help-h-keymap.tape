# The focused default footer, the ? peek above it, and the full key map under
# H. A single ? sticks until the next key. Repeated ? presses stand in for a
# held key, then disappear after the release window.

Screenshot {{SHOTS}}/01-focused-default-footer.png
Sleep 1s

Type "?"
Sleep 700ms
Screenshot {{SHOTS}}/02-tap-peek-stays-open.png
Sleep 1s

Type "x"
Sleep 1s

Type@30ms "????????????"
Screenshot {{SHOTS}}/03-hold-peek-visible.png
Sleep 600ms
Screenshot {{SHOTS}}/04-hold-peek-released.png
Sleep 1s

Type "H"
Sleep 2s
Screenshot {{SHOTS}}/05-key-map-under-H.png
Sleep 1s

Type "/"
Sleep 600ms
Type "triage"
Sleep 1s
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/06-triage-rows.png
Sleep 1s

Type "H"
Sleep 2s
Screenshot {{SHOTS}}/07-H-closed-it.png
Sleep 1s
