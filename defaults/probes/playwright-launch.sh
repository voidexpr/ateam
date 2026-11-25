#!/bin/sh
# id: playwright-launch
# group: playwright
# target: playwright-cli launch
# why: a browser that starts can be driven by the command
# starts: a headless browser session with playwright-cli on about:blank, closed at once
# risk: none
# needs: playwright-cli

session="ateam-probe-$$"
trap 'playwright-cli -s="$session" close >/dev/null 2>&1' EXIT

if playwright-cli -s="$session" open about:blank >/dev/null 2>&1; then
	echo "a browser started"
	exit 0
fi
echo "the browser did not start"
exit 1
