# Each rail row shows what its session is about after the name. Run with
#   --foreign list-summary --cols 200 --rows 40
# The transcript carries a title longer than the name the sweep derives from
# it, so the row keeps the title as its summary; the other seeded rows have
# no title or prompt and draw none. \ hides the preview so the list gets the
# width a summary needs; beside the preview it yields to the meta.

Sleep 35s
Escape
Sleep 2s
Screenshot {{SHOTS}}/01-rail-beside-preview.png
Sleep 1s
Type "\"
Sleep 2s
Screenshot {{SHOTS}}/02-rail-summary.png
Sleep 1s
