#!/usr/bin/env bash
# apt-install.sh PACKAGE...: apt-get update and install PACKAGE... (without
# recommends) for the CI and release workflows, tried again when Ubuntu's
# servers do not answer: after a failed round it waits, and doubles the
# wait each round (10 s, 20 s, 40 s, ...). APT_TRIES rounds in all (7 by
# default: at most 10.5 minutes of waiting), the first wait APT_WAIT
# seconds (10). Within a round apt itself tries each file 3 times.
set -uo pipefail

if [ $# -eq 0 ]; then
    echo "usage: apt-install.sh PACKAGE..." >&2
    exit 2
fi
tries=${APT_TRIES:-7}
wait=${APT_WAIT:-10}

for ((round = 1; ; round++)); do
    if sudo apt-get update -o Acquire::Retries=3 &&
        sudo apt-get install -y --no-install-recommends -o Acquire::Retries=3 "$@"; then
        exit 0
    fi
    if [ "$round" -ge "$tries" ]; then
        echo "apt-install.sh: round $round of $tries failed; giving up" >&2
        exit 1
    fi
    echo "apt-install.sh: round $round of $tries failed; trying again in ${wait} s" >&2
    sleep "$wait"
    wait=$((wait * 2))
done
