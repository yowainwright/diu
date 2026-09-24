package core

const wrapperRecordingStart = `START_TIME=$(/bin/date +%s)

"$DIU_ORIGINAL" "$@"
EXIT_CODE=$?

# Admit recording before detaching: excess events are dropped, never queued.
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

printf '%s\n' "$payload" | DIU_RECORDING=1 "$DIU_RECORD_BINARY" record --background >/dev/null 2>&1
} </dev/null >/dev/null 2>&1

exit $EXIT_CODE
`

func WrapperRecordingScript(payload string) string {
	script := wrapperRecordingStart + payload + wrapperRecordingEnd
	return script
}
