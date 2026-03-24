#!/bin/bash
set -e

MODE="${1:-prod}"
TARGET="${2:-}"
TAGS=""

# Parse cross-compilation target (os/arch, e.g. windows/amd64)
if [ -n "$TARGET" ]; then
  GOOS="${TARGET%%/*}"
  GOARCH="${TARGET#*/}"
  if [ "$GOARCH" = "$GOOS" ]; then
    GOARCH="amd64"
  fi
  export GOOS GOARCH
  SUFFIX="${GOOS}-${GOARCH}"
  EXT=""
  [ "$GOOS" = "windows" ] && EXT=".exe"
  OUTPUT="gopher-rdp-${SUFFIX}${EXT}"

  # macOS cross-compilation requires CGO for the GUI (ebiten uses Metal/OpenGL).
  # Enable CGO only if a C cross-compiler is available (e.g. via osxcross).
  if [ "$GOOS" = "darwin" ]; then
    if [ -z "$CC" ]; then
      # Look for common osxcross compiler names
      for cc in o64-clang x86_64-apple-darwin-clang aarch64-apple-darwin-clang; do
        if command -v "$cc" &>/dev/null; then
          export CC="$cc"
          break
        fi
      done
    fi
    if [ -n "$CC" ]; then
      export CGO_ENABLED=1
    else
      echo "WARNING: No macOS C cross-compiler found (CC not set)."
      echo "         Building with CGO_ENABLED=0 — GUI (ebiten) will not work."
      echo "         Install osxcross and set CC to enable full macOS builds."
      export CGO_ENABLED=0
    fi
  fi
else
  OUTPUT="gopher-rdp"
fi

OUTDIR="dist"

# build_one builds a single binary.
# Usage: build_one <mode> <goos> <goarch>
build_one() {
  local mode="$1" goos="$2" goarch="$3"
  local ext="" tags="" cgo_env=""
  [ "$goos" = "windows" ] && ext=".exe"
  local out="${OUTDIR}/gopher-rdp-${goos}-${goarch}${ext}"

  case "$mode" in
    debug)
      tags="gui"
      echo "==> Building ${goos}/${goarch} (debug+gui)..."
      GOOS="$goos" GOARCH="$goarch" go build -tags "$tags" -gcflags='all=-N -l' -o "$out" ./client
      ;;
    prod)
      tags="gui"
      echo "==> Building ${goos}/${goarch} (production+gui)..."
      GOOS="$goos" GOARCH="$goarch" go build -tags "$tags" -ldflags='-s -w' -trimpath -o "$out" ./client
      ;;
    web)
      echo "==> Building ${goos}/${goarch} (web-only)..."
      CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -ldflags='-s -w' -trimpath -o "$out" ./client
      ;;
  esac
  echo "    -> ${out}"
}

case "$MODE" in
  debug)
    TAGS="gui"
    echo "==> Building (debug+gui${TARGET:+, ${GOOS}/${GOARCH}})..."
    go build -tags "$TAGS" -gcflags='all=-N -l' -o "$OUTPUT" ./client
    echo "==> Done: ./${OUTPUT} (debug, gui, no optimizations)"
    ;;
  prod)
    TAGS="gui"
    echo "==> Building (production+gui${TARGET:+, ${GOOS}/${GOARCH}})..."
    go build -tags "$TAGS" -ldflags='-s -w' -trimpath -o "$OUTPUT" ./client
    echo "==> Done: ./${OUTPUT} (production, gui, stripped)"
    ;;
  web)
    echo "==> Building (web-only/headless${TARGET:+, ${GOOS}/${GOARCH}})..."
    CGO_ENABLED=0 go build -ldflags='-s -w' -trimpath -o "$OUTPUT" ./client
    echo "==> Done: ./${OUTPUT} (web-only, no GUI, no CGO)"
    ;;
  race)
    echo "==> Building (race detector, web-only)..."
    go build -race -o "${OUTPUT}-race" ./client
    echo "==> Done: ./${OUTPUT}-race (race detector enabled)"
    ;;
  race-gui)
    TAGS="gui"
    echo "==> Building (race detector, gui${TARGET:+, ${GOOS}/${GOARCH}})..."
    go build -race -tags "$TAGS" -o "${OUTPUT}-race" ./client
    echo "==> Done: ./${OUTPUT}-race (race detector, gui)"
    ;;
  test)
    echo "==> Vetting..."
    go vet ./...
    echo "==> Testing..."
    go test -count=1 ./...
    ;;
  test-race)
    echo "==> Vetting..."
    go vet ./...
    echo "==> Testing (race detector)..."
    go test -race -count=1 ./...
    ;;
  all)
    # Build web-only (CGO_ENABLED=0) binaries for all major platforms.
    # GUI builds require platform-specific C toolchains and are skipped here;
    # use "prod <os/arch>" for GUI builds with the appropriate CC set.
    mkdir -p "$OUTDIR"
    echo "==> Cross-compiling web-only binaries for all platforms into ${OUTDIR}/"
    echo ""
    build_one web linux   amd64
    build_one web linux   arm64
    build_one web windows amd64
    build_one web windows arm64
    build_one web darwin  amd64
    build_one web darwin  arm64
    echo ""
    echo "==> All builds complete:"
    ls -lh "${OUTDIR}"/gopher-rdp-*
    ;;
  *)
    echo "Usage: $0 [debug|prod|web|race|race-gui|test|test-race|all] [os/arch]"
    echo ""
    echo "Build modes:"
    echo "  debug     - GUI + web, no compiler optimizations, full symbol info (for dlv/gdb)"
    echo "  prod      - GUI + web, stripped, trimmed paths (default)"
    echo "  web       - Web-only, no GUI/X11/CGO dependency (headless/service)"
    echo "  race      - Web-only with race detector enabled"
    echo "  race-gui  - GUI + web with race detector enabled"
    echo "  all       - Cross-compile web-only binaries for all platforms into dist/"
    echo ""
    echo "Test modes:"
    echo "  test      - Run go vet + go test"
    echo "  test-race - Run go vet + go test with race detector"
    echo ""
    echo "Cross-compilation examples:"
    echo "  $0 prod windows/amd64"
    echo "  $0 prod linux/arm64"
    echo "  $0 prod darwin/arm64     # Apple Silicon (needs osxcross for GUI)"
    echo "  $0 prod darwin/amd64     # Intel Mac (needs osxcross for GUI)"
    echo "  $0 web linux/amd64       # Headless server, no X11 needed"
    echo "  $0 all                   # All platforms (web-only, no GUI)"
    echo "  $0 debug windows/386"
    exit 1
    ;;
esac
