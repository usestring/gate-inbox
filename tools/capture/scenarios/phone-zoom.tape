# The board on a phone with the mobile layout, recorded at the phone's size and
# again pinch-zoomed out on the same screen: the font shrinks, so the same
# pixels hold more cells. Mobile keeps one panel at both.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/phone-zoom.tape --cols 54 --rows 27
#   tools/capture/capture.sh --scenario tools/capture/scenarios/phone-zoom.tape --cols 90 --rows 45 --font-size 12
Sleep 2s
Screenshot {{SHOTS}}/01-auto.png
Sleep 1s
Type "s"
Sleep 1500ms
Down 6
Sleep 500ms
Right 2
Sleep 1s
Screenshot {{SHOTS}}/02-settings-mobile.png
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/03-mobile.png
Sleep 1s
