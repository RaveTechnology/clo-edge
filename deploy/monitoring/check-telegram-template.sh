#!/bin/sh
set -eu
CLO_TEMPLATE_RESULT="$(amtool template render \
  --template.glob=telegram.tmpl \
  --template.text='{{ template "clo.telegram" . }}' \
  --template.data=telegram-message.test.json)"
printf '%s\n' "$CLO_TEMPLATE_RESULT" | grep -F '[warning] RESOLVED:'
printf '%s\n' "$CLO_TEMPLATE_RESULT" | grep -F '[critical] FIRING:'
