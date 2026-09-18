printf 'LAST_CHAR_CHECK[' ; tail -c 1 /dev/stdin | od -c | head -2 ; echo ']'
echo SCRIPT_END
