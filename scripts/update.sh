#!/bin/bash
set -e

# Configuration
PBF_URL=${PBF_URL:-"http://download.geofabrik.de/europe/liechtenstein-latest.osm.pbf"}
CONFIG_PATH=${CONFIG_PATH:-"config.toml"}
BINARY_PATH=${BINARY_PATH:-"./poisearch"}
TEMP_DIR=$(mktemp -d)
NEW_INDEX_PATH="$TEMP_DIR/new_index.bleve"

echo "Using config: $CONFIG_PATH"
# Extract index path from config (simple grep for this script)
INDEX_PATH=$(grep "index_path" "$CONFIG_PATH" | cut -d'"' -f2)

if [ -z "$INDEX_PATH" ]; then
    echo "Error: Could not find index_path in $CONFIG_PATH"
    exit 1
fi

echo "Downloading latest PBF..."
curl -L -o "$TEMP_DIR/latest.osm.pbf" "$PBF_URL"

echo "Building new index at $NEW_INDEX_PATH..."
# We need to temporarily override the index_path in the config for the build command
# or use a temporary config file.
TMP_CONFIG="$TEMP_DIR/tmp_config.toml"
cp "$CONFIG_PATH" "$TMP_CONFIG"
# Use sed to replace the index_path. Works on Linux.
sed -i "s|index_path = .*|index_path = \"$NEW_INDEX_PATH\"|" "$TMP_CONFIG"

"$BINARY_PATH" --config "$TMP_CONFIG" build "$TEMP_DIR/latest.osm.pbf"

echo "Replacing old index with new index..."
# Backup old index just in case
if [ -d "$INDEX_PATH" ]; then
    mv "$INDEX_PATH" "${INDEX_PATH}.old"
fi
mv "$NEW_INDEX_PATH" "$INDEX_PATH"

echo "Sending SIGHUP to poisearch to reload index..."
# Find the PID of the running poisearch process
PID=$(pgrep -f "$BINARY_PATH .*serve" || true)

if [ -n "$PID" ]; then
    kill -HUP "$PID"
    echo "SIGHUP sent to process $PID"
else
    echo "Warning: poisearch process not found. You may need to start it manually."
fi

# Cleanup
rm -rf "$TEMP_DIR"
if [ -d "${INDEX_PATH}.old" ]; then
    rm -rf "${INDEX_PATH}.old"
fi

echo "Update complete!"
