#!/bin/bash
# Generate cyb3rdog logo assets

ASSETS_DIR="/home/cyb3rdog/.picoclaw/workspace/orgs/cyb3rclaw/code/cyb3rclaw-fork/assets"
WORK_DIR="/home/cyb3rdog/.picoclaw/workspace/orgs/cyb3rclaw/code/cyb3rclaw-fork/assets/logos"

mkdir -p "$WORK_DIR"
cd "$WORK_DIR"

# Create a cyber-themed logo with claw emoji and modern text
convert -size 512x512 xc:'#0a0a0f' \
    -gravity center \
    -fill '#00ffcc' \
    -font /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf \
    -pointsize 100 \
    -annotate +0-60 "🦞" \
    -fill '#00ffaa' \
    -pointsize 48 \
    -annotate +0+40 "CYB3RDOG" \
    -fill '#888899' \
    -pointsize 20 \
    -annotate +0+90 "AI Agent" \
    "cyb3rdog_logo.png"

# Create header banner for web UI
convert -size 800x200 xc:'#0a0a0f' \
    -gravity center \
    -fill '#00ffaa' \
    -font /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf \
    -pointsize 64 \
    -annotate +0-20 "CYB3RDOG" \
    -fill '#ff00aa' \
    -pointsize 28 \
    -annotate +0+40 "AI Agent Platform" \
    "cyb3rdog_header.png"

# Create favicon
convert "cyb3rdog_logo.png" -resize 96x96 "cyb3rdog_favicon.png"
convert "cyb3rdog_logo.png" -resize 32x32 "cyb3rdog_favicon_32.png"
convert "cyb3rdog_logo.png" -resize 180x180 "cyb3rdog_apple_touch.png"

# Terminal logo (smaller, for ASCII art style)
convert -size 256x256 xc:'#0a0a0f' \
    -gravity center \
    -fill '#00ffaa' \
    -pointsize 80 \
    -annotate +0+0 "🦞" \
    "cyb3rdog_term.png"

# Copy to assets root
cp "cyb3rdog_logo.png" "$ASSETS_DIR/cyb3rdog_logo.png"
cp "cyb3rdog_header.png" "$ASSETS_DIR/cyb3rdog_header.png"
cp "cyb3rdog_favicon.png" "$ASSETS_DIR/cyb3rdog_favicon.png"
cp "cyb3rdog_term.png" "$ASSETS_DIR/cyb3rdog_term.png"

echo "Logos generated:"
ls -la "$ASSETS_DIR"/cyb3rdog*