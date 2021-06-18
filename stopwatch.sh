#!/bin/sh

if [ -z "$1" ]; then
    now=$(date +%s)sec
elif [ -f "$1" ]; then
    now=$(basename "$1")
else
    echo "Cannot write to $1"
    exit 1
fi

logFile="./${now}"

log() {
    echo "$1" | tee -a "${logFile}"
}

echo "${now}"

while true; do 
    echo -n "> "
    read comment
    log "$(TZ=UTC date --date now-$now +%s),${comment}"
done
