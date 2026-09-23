# GitHub switched off (github-switched-off.toml). The parked demo agent echoes
# two tickets and a pull request; the rail draws the tickets and no row for the
# pull request, because nothing asks GitHub about it.

Enter
Sleep 1s
Type "ABC-135518"
Enter
Type "ABC-141768"
Enter
Type "https://github.com/example-org/sample-repo/pull/1978"
Enter
Sleep 1s
Ctrl+q
Sleep 1s

Type "W"
Sleep 7s
Screenshot {{SHOTS}}/01-github-switched-off.png
Sleep 1s
