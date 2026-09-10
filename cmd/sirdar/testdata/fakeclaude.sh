#!/bin/sh
# A stand-in for the `claude` binary in the CLI end-to-end test. It answers
# every session with the same canned triage document, whatever the prompt
# says.
#
# The one thing it does read from stdin is the prompt line Sirdar writes
# immediately after starting the process: consuming it keeps that write from
# blocking on a full pipe, and keeps this script from exiting into it and
# turning the write into EPIPE. SIRDAR_FAKE_DOC names the JSON document to
# hand back as the session's structured output; newlines are stripped so the
# whole result stays on one line, as the stream-json protocol requires.
set -e
IFS= read -r _prompt || true

doc=${SIRDAR_FAKE_DOC:?SIRDAR_FAKE_DOC must name a note document}

echo '{"type":"system","subtype":"init","session_id":"fake-session"}'
printf '%s' '{"type":"result","subtype":"success","is_error":false,"num_turns":3,"session_id":"fake-session","result":"done","total_cost_usd":0.02,"usage":{"input_tokens":1200,"output_tokens":800},"structured_output":'
tr -d '\n' < "$doc"
printf '%s\n' '}'
