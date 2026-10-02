#!/usr/bin/env bash
set -e

BOLD="$(tput bold 2>/dev/null || true)"
CYAN="$(tput setaf 6 2>/dev/null || true)"
GREEN="$(tput setaf 2 2>/dev/null || true)"
YELLOW="$(tput setaf 3 2>/dev/null || true)"
RED="$(tput setaf 1 2>/dev/null || true)"
RESET="$(tput sgr0 2>/dev/null || true)"

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

echo "${CYAN}›${RESET} Downloading ${BINARY_NAME}..."
if command -v curl >/dev/null 2>&1; then
    sudo curl -fsSL "${DOWNLOAD_URL}" -o "${INSTALL_DIR}/pgcaliper" 2>/dev/null || {
        echo "${YELLOW}▲ Prebuilt binary not found on remote. Building from source if Go is available...${RESET}"
        if command -v go >/dev/null 2>&1; then
            go install github.com/Shubham071122/pgcaliper/cmd/pgcaliper@latest
        else
            echo "${RED}✖ Go is not installed. Please download binary from GitHub Releases.${RESET}"
            exit 1
        fi
    }
fi

sudo chmod +x "${INSTALL_DIR}/pgcaliper"
echo "${GREEN}✓${RESET} pgcaliper installed successfully to ${BOLD}${INSTALL_DIR}/pgcaliper${RESET}"
echo "${CYAN}›${RESET} Run '${CYAN}pgcaliper init${RESET}' to get started."
