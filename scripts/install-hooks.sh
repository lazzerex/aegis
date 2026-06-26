#!/bin/sh
# Install git hooks. Run once after cloning.
HOOKS_DIR="$(git rev-parse --git-dir)/hooks"

cat > "$HOOKS_DIR/pre-push" << 'EOF'
#!/bin/sh
echo "Running pre-push checks (make check)..."
make check
if [ $? -ne 0 ]; then
    echo "Pre-push checks failed. Fix errors before pushing."
    exit 1
fi
EOF

chmod +x "$HOOKS_DIR/pre-push"
echo "Installed pre-push hook. Runs 'make check' before every push."
