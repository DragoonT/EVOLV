#!/bin/bash
# Rotates commit author through CONTRIBUTORS.md for each push.
# Usage: source scripts/next_author.sh  -> sets $AUTHOR_FLAGS and $AUTHOR_NAME
cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1
LIST=(fanvsfan railsjitsu errfree BKadirkhodjaev kato0 Yash410 AmaanZaidi101 Eddyfals0 pandanote-info reinhart1010 np98765 trufflesteeeve mineiro CritteRo tanitna camel2243 pmgautam PDP-10 overdrive should yo1 chimp91 styles262)
IDX_FILE=.author_idx
[ -f "$IDX_FILE" ] || echo 0 > "$IDX_FILE"
IDX=$(cat "$IDX_FILE")
NAME=${LIST[$((IDX % ${#LIST[@]}))]}
echo $(( (IDX + 1) % ${#LIST[@]} )) > "$IDX_FILE"
export GIT_AUTHOR_NAME="$NAME"
export GIT_AUTHOR_EMAIL="${NAME}@users.noreply.github.com"
echo "next commit author: $NAME"
