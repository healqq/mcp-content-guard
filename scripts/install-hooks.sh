#!/usr/bin/env sh
set -e

HOOK=.git/hooks/pre-commit

cat > "$HOOK" << 'EOF'
#!/usr/bin/env sh
set -e

if ! command -v golangci-lint > /dev/null 2>&1; then
  echo "golangci-lint not found — skipping lint (run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)"
  exit 0
fi

golangci-lint run . ./bench ./internal/...
EOF

chmod +x "$HOOK"
echo "pre-commit hook installed at $HOOK"
