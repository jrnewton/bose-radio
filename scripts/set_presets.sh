#!/bin/sh
# Rewrite all 6 SoundTouch presets to LOCAL_INTERNET_RADIO pointing at our Orion
# endpoint, using the locations our own service generates from presets.conf.
XML=$(curl -s "http://localhost:8000/streaming/account/1/device/X/presets")
for i in 1 2 3 4 5 6; do
  LOC=$(echo "$XML" | grep -o 'location="[^"]*"' | sed -n "${i}p" | sed 's/location="//;s/"$//')
  NAME=$(echo "$XML" | grep -o '<itemName>[^<]*</itemName>' | sed -n "${i}p" | sed 's/<itemName>//;s|</itemName>||')
  TS=$(date +%s)
  [ -z "$LOC" ] && { echo "preset $i: no location, skipping"; continue; }
  curl -s -X POST -H "Content-Type: application/xml" \
    -d "<preset id=\"$i\" createdOn=\"$TS\" updatedOn=\"$TS\"><ContentItem source=\"LOCAL_INTERNET_RADIO\" type=\"stationurl\" location=\"$LOC\" sourceAccount=\"\" isPresetable=\"true\"><itemName>$NAME</itemName></ContentItem></preset>" \
    http://localhost:8090/storePreset >/dev/null
  echo "stored preset $i: $NAME"
done
echo "=== presets now (source / name) ==="
curl -s http://localhost:8090/presets | grep -o 'source="[^"]*"\|<itemName>[^<]*</itemName>'
