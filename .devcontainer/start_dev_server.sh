#!/bin/bash

# Capture the output of the `arecord -l` command
arecord_output=$(arecord -l)

# Extract recording cards
card_names=$(echo "$arecord_output" | grep -oP 'card \d+: \w+ \[\K[^\]]+' | uniq)

# Generate dialog menu options
options=()
cards=()
index=1
while IFS= read -r line; do
    cards+=("$line")
    options+=($index "$line")
    ((index++))
done <<< "$card_names"

# Show dialog prompt to user
mic_to_use=$(dialog --clear --backtitle "Audio Device Selection" --title "Select an Audio Device" --menu "Choose one of the following options:" 15 40 4 "${options[@]}"  2>&1 >/dev/tty)

selected_mic=${cards[mic_to_use - 1]}

# Start dev server with selected mic
make dev_server REALTIME_ARGS="--source ${selected_mic}"
