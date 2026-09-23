package core

const wrapperRecordingStart = `START_TIME=$(/bin/date +%s)

"$DIU_ORIGINAL" "$@"
EXIT_CODE=$?

# Recording must not delay the command or retain its input/output pipes.
{
END_TIME=$(/bin/date +%s)
DURATION=$(( (END_TIME - START_TIME) * 1000 ))

json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%s' "$value"
}

args_json="["
first=true
for arg in "$@"; do
    if [ "$first" = true ]; then
        first=false
    else
        args_json="$args_json,"
    fi
    args_json="$args_json\"$(json_escape "$arg")\""
done
args_json="$args_json]"

payload=$(/bin/cat <<EOF
{
    "tool": "$DIU_TOOL",
    "command": "$(json_escape "$DIU_RECORD_COMMAND $*")",
    "args": $args_json,
    "exit_code": $EXIT_CODE,
    "duration_ms": $DURATION,
    "timestamp": "$(/bin/date -u +%Y-%m-%dT%H:%M:%SZ)",
    "working_dir": "$(json_escape "$(pwd)")",
    "user": "$(json_escape "$(/usr/bin/whoami)")",
`

const wrapperRecordingEnd = `}
EOF
)

record_fallback() {
    if [ -n "$DIU_RECORD_BINARY" ] && [ -x "$DIU_RECORD_BINARY" ]; then
        printf '%s\n' "$payload" | DIU_RECORDING=1 "$DIU_RECORD_BINARY" record >/dev/null 2>&1
    fi
}

# Use system nc so event delivery cannot enter a tracked wrapper.
if [ -S "$DIU_SOCKET" ] && [ -x /usr/bin/nc ]; then
    if ! printf '%s\n' "$payload" | /usr/bin/nc -w 1 -U "$DIU_SOCKET" 2>/dev/null; then
        record_fallback
    fi
else
    record_fallback
fi
} </dev/null >/dev/null 2>&1 &

exit $EXIT_CODE
`

func WrapperRecordingScript(payload string) string {
	script := wrapperRecordingStart + payload + wrapperRecordingEnd
	return script
}
