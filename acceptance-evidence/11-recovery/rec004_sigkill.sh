#!/usr/bin/env bash
set +e
wpid=$(pgrep -f 'bin/worker' | head -1)
echo "killing worker $wpid"
kill -9 $wpid
sleep 2
pgrep -af 'bin/worker' || echo "worker gone"
