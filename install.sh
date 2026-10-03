#!/usr/bin/env bash
set -e

BOLD="$(tput bold 2>/dev/null || true)"
CYAN="$(tput setaf 6 2>/dev/null || true)"
GREEN="$(tput setaf 2 2>/dev/null || true)"
YELLOW="$(tput setaf 3 2>/dev/null || true)"
RED="$(tput setaf 1 2>/dev/null || true)"
RESET="$(tput sgr0 2>/dev/null || true)"

# Determine if sudo is available and required
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "${YELLOW}▲ Running as non-root user without sudo. Will attempt direct installation.${RESET}"
    fi
fi

if ! command -v curl >/dev/null 2>&1; then
    echo "${RED}✖ Error: curl is not installed. Please install curl (e.g., 'apt-get update && apt-get install -y curl') and try again.${RESET}"
    exit 1
fi

echo "${CYAN}›${RESET} ${BOLD}Installing pgcaliper...${RESET}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

if [ "$ARCH" = "x86_64" ]; then
    ARCH="amd64"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    ARCH="arm64"
else
    echo "${RED}✖ Error: Unsupported architecture: ${ARCH}${RESET}"
    exit 1
fi

BINARY_NAME="pgcaliper-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/Shubham071122/pgcaliper/releases/latest/download/${BINARY_NAME}"
INSTALL_DIR="/usr/local/bin"

mkdir -p "${INSTALL_DIR}" 2>/dev/null || $SUDO mkdir -p "${INSTALL_DIR}"

echo "${CYAN}›${RESET} Downloading ${BINARY_NAME} from GitHub Releases..."
INSTALLED=false

if $SUDO curl -fL --progress-bar "${DOWNLOAD_URL}" -o "${INSTALL_DIR}/pgcaliper"; then
    INSTALLED=true
fi

if [ "$INSTALLED" = false ]; then
    echo "${YELLOW}▲ Prebuilt release binary not found. Building from source with Go...${RESET}"
    if command -v go >/dev/null 2>&1; then
        GOBIN="$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"
        go install github.com/Shubham071122/pgcaliper/cmd/pgcaliper@latest
        $SUDO cp "${GOBIN}/pgcaliper" "${INSTALL_DIR}/pgcaliper"
        INSTALLED=true
    else
        echo "${RED}✖ Go is not installed. Please download precompiled binary from https://github.com/Shubham071122/pgcaliper/releases${RESET}"
        exit 1
    fi
fi

$SUDO chmod +x "${INSTALL_DIR}/pgcaliper"
echo "${GREEN}✓${RESET} pgcaliper installed successfully to ${BOLD}${INSTALL_DIR}/pgcaliper${RESET}"
echo "${CYAN}›${RESET} Run '${CYAN}pgcaliper --help${RESET}' or '${CYAN}pgcaliper init${RESET}' to get started."
