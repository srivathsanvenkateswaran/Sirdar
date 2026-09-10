#!/bin/sh
# The rca counterpart of fakeclaude.sh: same protocol, but it defaults to the
# combined rca+resolution document when SIRDAR_FAKE_DOC is not set.
set -e
IFS= read -r _prompt || true

doc=${SIRDAR_FAKE_DOC:-$(dirname "$0")/rca-doc.json}

echo '{"type":"system","subtype":"init","session_id":"fake-rca-session"}'
printf '%s' '{"type":"result","subtype":"success","is_error":false,"num_turns":4,"session_id":"fake-rca-session","result":"done","total_cost_usd":0.03,"usage":{"input_tokens":2400,"output_tokens":1600},"structured_output":'
tr -d '\n' < "$doc"
printf '%s\n' '}'
