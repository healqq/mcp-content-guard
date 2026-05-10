#!/usr/bin/env bash
set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="$SCRIPT_DIR/data"

echo "=== mcp-context-guard bench setup ==="

echo "1/3  Generating SQLite database..."
python3 "$SCRIPT_DIR/data/gen_db.py"

REACT_DIR="$DATA_DIR/react-repo"
if [ -d "$REACT_DIR/.git" ]; then
    echo "2/3  React repo already present at $REACT_DIR"
else
    echo "2/3  Cloning React v18.2.0 (depth 300)..."
    git clone --depth=300 --branch v18.2.0 \
        https://github.com/facebook/react.git "$REACT_DIR"
fi

echo "3/3  Installing Node dependencies..."
cd "$SCRIPT_DIR/eval"
npm install

echo ""
echo "Done. Set OPENAI_API_KEY then run:"
echo "  cd bench/eval"
echo "  npx promptfoo eval -c sqlite.yaml    --output results-sqlite.json"
echo "  npx promptfoo eval -c filesystem.yaml --output results-filesystem.json"
echo "  npx promptfoo eval -c playwright.yaml  --output results-playwright.json"
echo "  npx promptfoo eval -c git.yaml         --output results-git.json"
echo "  node compare.js results-*.json"
