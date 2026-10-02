# The board on a phone held upright with its keyboard up: about 48 columns by
# 25 rows. Records the list as it settles, then with the cursor moved.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/phone-board.tape \
#     --cols 48 --rows 25 --foreign waiting --foreign finished --foreign working \
#     --foreign waiting-long --foreign resting
Sleep 6s
Screenshot {{SHOTS}}/01-phone-board.png
Sleep 1s
Type "k"
Sleep 1500ms
Type "k"
Sleep 2s
Screenshot {{SHOTS}}/02-phone-board-cursor.png
Sleep 1s
