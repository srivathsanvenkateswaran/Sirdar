#!/bin/sh
# A stand-in stdio MCP server for the tests of `sirdar mcp` and of the API
# routes behind it. It speaks the subset Sirdar's client sends — initialize,
# notifications/initialized, tools/list, tools/call — one JSON object per
# line, and nothing else.
#
# The frames are matched by hand rather than parsed: the client marshals
# every request from one struct, so "id" always precedes "method" and the
# shapes below are the only ones that arrive. That is what lets this be a
# shell script instead of a compiled fixture.
#
# It deliberately exposes one read-shaped tool, one write-shaped tool and
# one generic passthrough, so a test can assert each verdict against a real
# server rather than a made-up tool list. FAKE_MCP_TOKEN, if set, is the
# credential the entry handed it; `echo_token` hands it straight back, which
# is how the redaction is tested.
set -e

reply() {
	printf '{"jsonrpc":"2.0","id":%s,"result":%s}\n' "$1" "$2"
}

while IFS= read -r line; do
	id=$(printf '%s' "$line" | sed -n 's/^{"jsonrpc":"2.0","id":\([0-9]*\).*/\1/p')
	method=$(printf '%s' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
	case "$method" in
	initialize)
		reply "$id" '{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-mcp","version":"1.2.3"}}'
		;;
	notifications/initialized)
		echo "fake mcp server: ready" >&2
		;;
	tools/list)
		reply "$id" '{"tools":[{"name":"list_rows","description":"read some rows","inputSchema":{"type":"object","properties":{"table":{"type":"string"}}}},{"name":"delete_rows","description":"delete some rows","inputSchema":{"type":"object"}},{"name":"api_request","description":"send anything anywhere","inputSchema":{"type":"object"}},{"name":"echo_token","description":"get the token back","inputSchema":{"type":"object"}}]}'
		;;
	tools/call)
		name=$(printf '%s' "$line" | sed -n 's/.*"params":{"arguments":\(.*\),"name":"\([^"]*\)".*/\2/p')
		if [ -z "$name" ]; then
			name=$(printf '%s' "$line" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
		fi
		case "$name" in
		echo_token)
			reply "$id" "{\"content\":[{\"type\":\"text\",\"text\":\"token is ${FAKE_MCP_TOKEN}\"}]}"
			;;
		list_rows)
			args=$(printf '%s' "$line" | sed -n 's/.*"table":"\([^"]*\)".*/\1/p')
			reply "$id" "{\"content\":[{\"type\":\"text\",\"text\":\"rows of ${args:-nothing}\"}]}"
			;;
		*)
			reply "$id" '{"content":[{"type":"text","text":"ok"}]}'
			;;
		esac
		;;
	esac
done
