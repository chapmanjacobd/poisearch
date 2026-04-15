#!/bin/bash
set -e

# Configuration
PLANETILER_VERSION="0.10.2"
PMTILES_VERSION="1.30.1"
AREA=${1:-"liechtenstein"}
PBF_FILE=${2:-"${AREA}-latest.osm.pbf"}
OUTPUT_FILE=${3:-"${AREA}.pmtiles"}
SCHEMA=${4:-"openmaptiles"}

# Directories
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPTS_DIR")"
BIN_DIR="$SCRIPTS_DIR/bin"
mkdir -p "$BIN_DIR"

# Tools
PLANETILER_JAR="$BIN_DIR/planetiler.jar"
PMTILES_BIN="$BIN_DIR/pmtiles"

# Download planetiler if missing
if [ ! -f "$PLANETILER_JAR" ]; then
    echo "Downloading planetiler v$PLANETILER_VERSION..."
    curl -L "https://github.com/onthegomap/planetiler/releases/download/v$PLANETILER_VERSION/planetiler.jar" -o "$PLANETILER_JAR"
fi

# Download pmtiles if missing
if [ ! -f "$PMTILES_BIN" ]; then
    echo "Downloading pmtiles v$PMTILES_VERSION..."
    # Determine OS/Arch
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64) ARCH="x86_64" ;;
        aarch64) ARCH="arm64" ;;
    esac
    
    # URL format is like go-pmtiles_1.30.1_Linux_x86_64.tar.gz but OS is TitleCase
    OS_TITLE=$(echo "$OS" | sed 's/.*/\u&/')
    # Special case for Darwin naming with hyphen sometimes, but Linux uses underscore
    URL="https://github.com/protomaps/go-pmtiles/releases/download/v$PMTILES_VERSION/go-pmtiles_${PMTILES_VERSION}_${OS_TITLE}_${ARCH}.tar.gz"
    echo "Downloading from $URL..."
    
    TEMP_TGZ=$(mktemp)
    if ! curl -fL "$URL" -o "$TEMP_TGZ"; then
        echo "Error downloading pmtiles from $URL"
        exit 1
    fi
    tar -xzf "$TEMP_TGZ" -C "$BIN_DIR" pmtiles
    rm "$TEMP_TGZ"
fi

# Bounds (to make generation faster and smaller)
# minlon, minlat, maxlon, maxlat
if [ "$AREA" = "liechtenstein" ]; then
    BOUNDS="9.47,47.04,9.65,47.28"
    BOUNDS_ARG="--bounds=$BOUNDS"
elif [ "$AREA" = "taiwan" ]; then
    BOUNDS="119.3,21.7,122.3,25.5"
    BOUNDS_ARG="--bounds=$BOUNDS"
else
    BOUNDS_ARG=""
fi

# Use local PBF if it exists, otherwise rely on planetiler download
if [ -f "$ROOT_DIR/$PBF_FILE" ]; then
    OSM_PATH="--osm-path=$ROOT_DIR/$PBF_FILE"
    DOWNLOAD_ARG=""
    echo "Using local OSM PBF: $PBF_FILE"
else
    OSM_PATH=""
    DOWNLOAD_ARG="--download"
    echo "Local PBF not found, planetiler will download for area: $AREA"
fi

echo "Generating PMTiles for $AREA using $SCHEMA schema..."

# Note: Using java -jar planetiler.jar
# We output directly to .pmtiles
java -Xmx2g -jar "$PLANETILER_JAR" \
    $OSM_PATH \
    --output="$ROOT_DIR/$OUTPUT_FILE" \
    $BOUNDS_ARG \
    --area="$AREA" \
    $DOWNLOAD_ARG \
    --fetch-wikidata \
    --nodemap-type=sparsearray \
    --schema="$SCHEMA" \
    --log-jts-exceptions=true

echo "Done! PMTiles file created at $ROOT_DIR/$OUTPUT_FILE"
